package assistant

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// triggerChanges names what an edit moved, in the order the form asks for the
// fields, empty when nothing moved. Reactor.Edit answers it, so the line
// `trigger-edit` prints and the toast the page shows are the one wording.
func triggerChanges(before, after Trigger) string {
	var out []string
	if before.Name != after.Name {
		if after.Name == "" {
			out = append(out, "no name")
		} else {
			out = append(out, "name "+after.Name)
		}
	}
	if before.Task != after.Task {
		out = append(out, "task")
	}
	if before.Model != after.Model {
		if after.Model == "" {
			out = append(out, "the assistant default")
		} else {
			out = append(out, "model "+after.Model)
		}
	}
	if before.Effort != after.Effort {
		if after.Effort == "" {
			out = append(out, "the assistant's effort")
		} else {
			out = append(out, "effort "+after.Effort)
		}
	}
	if !slices.Equal(before.Terminals(), after.Terminals()) {
		if len(after.Targets) == 0 {
			out = append(out, "no terminal")
		} else {
			out = append(out, fmt.Sprintf("%d terminal%s", len(after.Targets), plural(len(after.Targets))))
		}
	}
	if before.All != after.All {
		if after.All {
			out = append(out, "all of them")
		} else {
			out = append(out, "any of them")
		}
	}
	if before.Spec != after.Spec {
		out = append(out, "schedule "+after.Spec)
	}
	if before.Timezone != after.Timezone {
		out = append(out, "zone "+after.Timezone)
	}
	if before.Once != after.Once {
		if after.Once {
			out = append(out, "once")
		} else {
			out = append(out, "standing")
		}
	}
	if !before.ExpiresAt.Equal(after.ExpiresAt) {
		if after.ExpiresAt.IsZero() {
			out = append(out, "no expiry")
		} else {
			out = append(out, "until "+after.ExpiresAt.In(time.Local).Format("2006-01-02 15:04"))
		}
	}
	if before.BatchSeconds != after.BatchSeconds {
		out = append(out, fmt.Sprintf("batch %ds", after.BatchSeconds))
	}
	return strings.Join(out, ", ")
}
