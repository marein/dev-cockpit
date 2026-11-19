package pluginhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ManifestFile is the manifest's name in an external plugin's directory.
const ManifestFile = "plugin.json"

// Protocol is the external plugin protocol version, see the README. A
// manifest names the version it was written for.
const Protocol = 1

// idPattern matches every plugin id, compiled or external. It stays this
// narrow because the id prefixes the final custom element names.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidID answers whether id is a valid plugin id, compiled or external.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// maxExternalID bounds an external id, it names the plugin's socket
// directory and a unix socket path has a small fixed limit.
const maxExternalID = 40

// Bounds of a manifest against hostile or broken files.
const (
	maxManifestBytes = 64 << 10
	maxNameRunes     = 64
	maxVersionRunes  = 32
	maxDescription   = 300
	maxPathBytes     = 256
	maxElements      = 16
	maxMounts        = 8
	maxMarkupBytes   = 64 << 10
	maxSettings      = 64
	maxSettingKey    = 128
	maxSettingValue  = 64 << 10
)

// Manifest is an external plugin's plugin.json. The cockpit applies it
// through the Adds a compiled plugin calls, the process behind Run answers
// the routes.
type Manifest struct {
	Protocol    int               `json:"protocol"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Run         string            `json:"run"`
	Assets      string            `json:"assets"`
	Elements    []ManifestElement `json:"elements"`
	Slots       []ManifestSlot    `json:"slots"`
	Page        *ManifestPage     `json:"page"`
	Settings    *ManifestSettings `json:"settings"`
	Memory      *ManifestMemory   `json:"memory"`
	Events      []string          `json:"events"`
}

// ManifestElement is one AddCustomElement call.
type ManifestElement struct {
	Name   string `json:"name"`
	Module string `json:"module"`
}

// ManifestSlot is one AddSlotHTML call, HTML names a file in the plugin
// directory.
type ManifestSlot struct {
	Slot string `json:"slot"`
	HTML string `json:"html"`
}

// ManifestPage is AddPage, HTML names the body's file in the plugin
// directory.
type ManifestPage struct {
	Title    string `json:"title"`
	Icon     string `json:"icon"`
	NavLabel string `json:"nav_label"`
	HTML     string `json:"html"`
}

// ManifestSettings is AddSettings.
type ManifestSettings struct {
	Values    map[string]string `json:"values"`
	Overwrite bool              `json:"overwrite"`
}

// ManifestMemory is AddAssistantMemory, Dir names a directory in the plugin
// directory.
type ManifestMemory struct {
	Dir       string `json:"dir"`
	Overwrite bool   `json:"overwrite"`
}

var elementName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ParseManifest reads and checks a manifest. Every refusal is one sentence
// naming the field, for the Plugins page.
func ParseManifest(r io.Reader) (*Manifest, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("The manifest could not be read: %v.", err)
	}
	if len(raw) > maxManifestBytes {
		return nil, errors.New("The manifest is larger than 64 KiB.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("The manifest is invalid: %v.", err)
	}
	if decoder.More() {
		return nil, errors.New("The manifest has more than one JSON value.")
	}
	if err := m.check(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) check() error {
	if m.Protocol != Protocol {
		return fmt.Errorf("The manifest uses protocol %d, the cockpit supports %d.", m.Protocol, Protocol)
	}
	if !ValidID(m.ID) || len(m.ID) > maxExternalID {
		return fmt.Errorf("The id must be lowercase words joined by dashes, at most %d characters.", maxExternalID)
	}
	if err := checkText("name", m.Name, maxNameRunes, true); err != nil {
		return err
	}
	if err := checkText("version", m.Version, maxVersionRunes, true); err != nil {
		return err
	}
	if err := checkText("description", m.Description, maxDescription, false); err != nil {
		return err
	}
	if err := checkLocalPath("run", m.Run, true); err != nil {
		return err
	}
	if err := checkLocalPath("assets", m.Assets, false); err != nil {
		return err
	}
	if len(m.Elements) > maxElements {
		return fmt.Errorf("A plugin has at most %d elements.", maxElements)
	}
	var names []string
	for _, e := range m.Elements {
		if !elementName.MatchString(e.Name) || len(e.Name) > 32 {
			return fmt.Errorf("The element name %q must be lowercase words joined by dashes.", cut(e.Name))
		}
		if slices.Contains(names, e.Name) {
			return fmt.Errorf("The element %s is declared twice.", e.Name)
		}
		names = append(names, e.Name)
		if m.Assets == "" {
			return errors.New("An element needs an assets directory for its module.")
		}
		if err := checkLocalPath("module of "+e.Name, e.Module, true); err != nil {
			return err
		}
		if !strings.HasSuffix(e.Module, ".js") {
			return fmt.Errorf("The module of %s must be a .js file.", e.Name)
		}
	}
	if len(m.Slots) > maxMounts {
		return fmt.Errorf("A plugin uses at most %d slots.", maxMounts)
	}
	for _, slot := range m.Slots {
		if !definedSlots[slot.Slot] {
			return fmt.Errorf("There is no slot named %q.", cut(slot.Slot))
		}
		if err := checkLocalPath("html of the slot "+slot.Slot, slot.HTML, true); err != nil {
			return err
		}
	}
	if p := m.Page; p != nil {
		if err := checkText("page title", p.Title, maxNameRunes, true); err != nil {
			return err
		}
		if !tablerIcon.MatchString(p.Icon) {
			return errors.New("The page icon must be a Tabler icon class like ti-box.")
		}
		if err := checkText("page nav_label", p.NavLabel, 32, false); err != nil {
			return err
		}
		if err := checkLocalPath("page html", p.HTML, true); err != nil {
			return err
		}
	}
	if s := m.Settings; s != nil {
		if len(s.Values) > maxSettings {
			return fmt.Errorf("A plugin adds at most %d settings.", maxSettings)
		}
		for key, value := range s.Values {
			if strings.TrimSpace(key) == "" || len(key) > maxSettingKey || hasControl(key) || strings.ContainsFunc(key, unicode.IsSpace) {
				return errors.New("A settings key is a word without spaces, at most 128 bytes.")
			}
			if len(value) > maxSettingValue || !utf8.ValidString(value) {
				return fmt.Errorf("The value of the setting %s is larger than 64 KiB or not UTF-8.", key)
			}
		}
	}
	if mem := m.Memory; mem != nil {
		if err := checkLocalPath("memory dir", mem.Dir, true); err != nil {
			return err
		}
	}
	for i, source := range m.Events {
		if !definedSources[source] || slices.Contains(m.Events[:i], source) {
			return fmt.Errorf("The event source %q is unknown or listed twice, use job, coder or compose.", cut(source))
		}
	}
	return nil
}

// checkText gates a free text field: bounded UTF-8 without control
// characters, which no label legitimately holds.
func checkText(field, value string, maxRunes int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("The %s is missing.", field)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes || hasControl(value) {
		return fmt.Errorf("The %s must be plain text of at most %d characters.", field, maxRunes)
	}
	return nil
}

// checkLocalPath gates a path inside the plugin directory: relative, local
// and bounded. Symlinks are resolved when the plugin loads.
func checkLocalPath(field, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("The %s path is missing.", field)
		}
		return nil
	}
	if len(value) > maxPathBytes || hasControl(value) || !filepath.IsLocal(value) || !fs.ValidPath(value) {
		return fmt.Errorf("The %s path must be a relative path inside the plugin directory.", field)
	}
	return nil
}

func hasControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

// cut bounds a value a refusal quotes.
func cut(value string) string { return cutTo(value, 40) }

// cutTo bounds value to limit bytes, cut at a rune start and ended with an
// ellipsis.
func cutTo(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "…"
}
