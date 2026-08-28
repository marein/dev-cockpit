package docker

import (
	"bufio"
	"hash/fnv"
	"io"
	"regexp"
	"strings"
)

// The log formatter is what the cockpit's log terminals pipe `docker logs -f`
// through: a severity gutter in front of every line, a stable tint per compose
// service, and with a pattern only the matching lines plus their context. It
// writes one line per write call, so a followed log streams live instead of
// sitting in a buffer.

// logSeverity is what one line's gutter block says: an error, a warning, or
// nothing that stands out.
type logSeverity int

const (
	logNeutral logSeverity = iota
	logWarn
	logError
)

// logGutterGlyph is the block at the start of every line. Only the glyph is
// colored, never the text behind it, so a run of consecutive errors stays
// readable.
const logGutterGlyph = "▍"

const (
	sgrReset   = "\x1b[0m"
	sgrDim     = "\x1b[2m"
	sgrRed     = "\x1b[31m"
	sgrYellow  = "\x1b[33m"
	sgrInverse = "\x1b[7m"
	sgrPlain   = "\x1b[27m"
)

func (s logSeverity) gutter() string {
	switch s {
	case logError:
		return sgrRed + logGutterGlyph + sgrReset + " "
	case logWarn:
		return sgrYellow + logGutterGlyph + sgrReset + " "
	}
	return sgrDim + logGutterGlyph + sgrReset + " "
}

// logServicePalette are the tints a service can get, 256 color codes picked to
// stay apart from the red and yellow the gutter means something by. Which
// service gets which is a hash of its name, so the color survives a restart
// and a reordering of the stream.
var logServicePalette = []string{
	"\x1b[38;5;75m",
	"\x1b[38;5;79m",
	"\x1b[38;5;108m",
	"\x1b[38;5;110m",
	"\x1b[38;5;114m",
	"\x1b[38;5;116m",
	"\x1b[38;5;139m",
	"\x1b[38;5;143m",
	"\x1b[38;5;152m",
	"\x1b[38;5;172m",
	"\x1b[38;5;176m",
	"\x1b[38;5;180m",
}

func logServiceTint(service string) string {
	h := fnv.New32a()
	h.Write([]byte(service))
	return logServicePalette[h.Sum32()%uint32(len(logServicePalette))]
}

// logPrefix is the compose style line head, `service-1  | rest`: the container
// name, padding, the pipe. A single container's `docker logs` writes no such
// prefix, and those lines get no tint.
var logPrefix = regexp.MustCompile(`^([^\s|]+) +\| ?`)

// splitLogPrefix answers the service of a compose prefixed line and the part
// behind the prefix, which is what the severity is read from. A line without
// the prefix answers an empty service and itself.
func splitLogPrefix(line string) (service, rest string) {
	loc := logPrefix.FindStringSubmatchIndex(line)
	if loc == nil {
		return "", line
	}
	return line[loc[2]:loc[3]], line[loc[1]:]
}

// logLevelField finds an explicit level, logfmt `level=...` or a JSON "level"
// field. Where one stands it decides alone: a line that says level=info talks
// about errors without being one.
var (
	logLevelField = regexp.MustCompile(`(?i)\blevel"?\s*[=:]\s*"?([a-z]+)`)
	logErrorToken = regexp.MustCompile(`(?i)\b(?:error|fatal|panic)\b`)
	logWarnToken  = regexp.MustCompile(`(?i)\bwarn(?:ing)?\b`)
)

func logLineSeverity(text string) logSeverity {
	if m := logLevelField.FindStringSubmatch(text); m != nil {
		switch strings.ToLower(m[1]) {
		case "error", "err", "fatal", "panic", "crit", "critical":
			return logError
		case "warn", "warning":
			return logWarn
		}
		return logNeutral
	}
	if logErrorToken.MatchString(text) {
		return logError
	}
	if logWarnToken.MatchString(text) {
		return logWarn
	}
	return logNeutral
}

// CompileLogPattern compiles a --grep pattern the way the formatter matches
// it, as a case insensitive regular expression. The handlers that spawn a log
// shell compile it too, so a broken pattern is refused where it was typed
// instead of failing inside the spawned pipeline. An empty pattern is no
// pattern.
func CompileLogPattern(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	return regexp.Compile("(?i)" + pattern)
}

// FormatLogs reads raw docker log lines from r and writes them formatted to
// w until the stream ends. Every output line is a single write, which is what
// keeps a followed log live when w is a pipe or a terminal.
func FormatLogs(r io.Reader, w io.Writer, pattern string, context int) error {
	grep, err := CompileLogPattern(pattern)
	if err != nil {
		return err
	}
	if context < 0 {
		context = 0
	}
	f := &logFormatter{w: w, grep: grep, context: context}
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if werr := f.line(strings.TrimRight(line, "\r\n")); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// logFormatter carries the grep state across lines: the unprinted lines that
// may yet become leading context, the count of trailing context still owed to
// the last match, and the number of the last printed line, which is what says
// whether two groups touch or a separator stands between them.
type logFormatter struct {
	w       io.Writer
	grep    *regexp.Regexp
	context int

	lineNo  int
	before  []pendingLogLine
	after   int
	printed int
}

type pendingLogLine struct {
	no   int
	text string
}

func (f *logFormatter) line(text string) error {
	f.lineNo++
	if f.grep == nil {
		return f.emit(f.lineNo, text, false)
	}
	if f.grep.MatchString(text) {
		if err := f.flushBefore(); err != nil {
			return err
		}
		f.after = f.context
		return f.emit(f.lineNo, text, true)
	}
	if f.after > 0 {
		f.after--
		return f.emit(f.lineNo, text, false)
	}
	f.before = append(f.before, pendingLogLine{no: f.lineNo, text: text})
	if len(f.before) > f.context {
		f.before = f.before[1:]
	}
	return nil
}

// flushBefore prints the leading context of a fresh match, with the group
// separator in front of it when lines were skipped since the last printed
// one, the way grep separates its groups.
func (f *logFormatter) flushBefore() error {
	first := f.lineNo
	if len(f.before) > 0 {
		first = f.before[0].no
	}
	if f.printed > 0 && first > f.printed+1 {
		if err := f.write("--"); err != nil {
			return err
		}
	}
	for _, pending := range f.before {
		if err := f.emit(pending.no, pending.text, false); err != nil {
			return err
		}
	}
	f.before = nil
	return nil
}

func (f *logFormatter) emit(no int, text string, highlight bool) error {
	f.printed = no
	return f.write(f.render(text, highlight))
}

func (f *logFormatter) write(line string) error {
	_, err := io.WriteString(f.w, line+"\n")
	return err
}

// render turns one raw line into its formatted shape: the severity gutter,
// then the line, tinted whole when it carries a compose prefix, with the grep
// matches inverted on a matching line. The inverse toggles sit inside the
// tint, so a highlighted match keeps its service color swapped onto the
// background.
func (f *logFormatter) render(text string, highlight bool) string {
	service, rest := splitLogPrefix(text)
	severity := logLineSeverity(rest)
	body := text
	if highlight {
		body = highlightLogMatches(body, f.grep)
	}
	if service != "" {
		body = logServiceTint(service) + body + sgrReset
	}
	return severity.gutter() + body
}

func highlightLogMatches(line string, re *regexp.Regexp) string {
	matches := re.FindAllStringIndex(line, -1)
	if len(matches) == 0 {
		return line
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		if m[0] == m[1] {
			continue
		}
		b.WriteString(line[last:m[0]])
		b.WriteString(sgrInverse)
		b.WriteString(line[m[0]:m[1]])
		b.WriteString(sgrPlain)
		last = m[1]
	}
	b.WriteString(line[last:])
	return b.String()
}
