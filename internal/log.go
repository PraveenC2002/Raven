package raven

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ---- ANSI color codes -----------------------------------------------------

const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	white   = "\033[37m"
)

// level controls per-message color, independent of the tag's color.
type level int

const (
	levelDebug level = iota
	levelInfo
	levelWarn
	levelError
)

func (l level) label() string {
	switch l {
	case levelDebug:
		return "DEBUG"
	case levelInfo:
		return "INFO"
	case levelWarn:
		return "WARN"
	case levelError:
		return "ERROR"
	default:
		return "????"
	}
}

func (l level) color() string {
	switch l {
	case levelDebug:
		return dim
	case levelInfo:
		return green
	case levelWarn:
		return yellow
	case levelError:
		return red
	default:
		return white
	}
}

// colorEnabled is computed once: only colorize when stdout is a real
// terminal, so piping logs to a file or another process gives plain text.
var colorEnabled = isTerminal(os.Stdout)

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func colorize(s, code string) string {
	if !colorEnabled {
		return s
	}
	return code + s + reset
}

// ---- Logger ----------------------------------------------------------------

// Logger is a tagged, colorized logger for one Raven component.
type Logger struct {
	tag      string
	tagColor string
	out      io.Writer
}

// New creates a Logger tagged with name, rendered in the given color.
func New(name, color string, out io.Writer) *Logger {
	return &Logger{tag: name, tagColor: color, out: out}
}

func (l *Logger) write(lvl level, msg string, kv ...any) {
	ts := time.Now().Format("15:04:05.000")
	tag := colorize(fmt.Sprintf("[%-12s]", l.tag), l.tagColor)
	lvlStr := colorize(fmt.Sprintf("%-5s", lvl.label()), lvl.color())

	var fields strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		fields.WriteString(fmt.Sprintf(" %s=%v", colorize(fmt.Sprint(kv[i]), cyan), kv[i+1]))
	}

	fmt.Fprintf(l.out, "%s %s %s %s%s\n", colorize(ts, dim), tag, lvlStr, msg, fields.String())
}

func (l *Logger) Debug(msg string, kv ...any) { l.write(levelDebug, msg, kv...) }
func (l *Logger) Info(msg string, kv ...any)  { l.write(levelInfo, msg, kv...) }
func (l *Logger) Warn(msg string, kv ...any)  { l.write(levelWarn, msg, kv...) }
func (l *Logger) Error(msg string, kv ...any) { l.write(levelError, msg, kv...) }

// Block renders a large multi-line payload (command output, API responses,
// stack traces, etc.) inside a clean bordered box instead of dumping raw
// text inline with single-line logs. title is shown in the box header.
func (l *Logger) Block(title string, body string) {
	const boxWidth = 64

	prefix := fmt.Sprintf("┌─ %s · %s ", l.tag, title)
	dashCount := boxWidth - len([]rune(prefix))
	if dashCount < 0 {
		dashCount = 0
	}

	header := colorize(fmt.Sprintf("┌─ %s ", l.tag), l.tagColor) +
		colorize(fmt.Sprintf("· %s ", title), bold) +
		colorize(strings.Repeat("─", dashCount), l.tagColor)
	footer := colorize(strings.Repeat("─", boxWidth), l.tagColor)

	fmt.Fprintln(l.out, header)
	body = strings.TrimRight(body, "\n")
	if body == "" {
		fmt.Fprintln(l.out, colorize("│ (empty)", dim))
	} else {
		for _, line := range strings.Split(body, "\n") {
			fmt.Fprintf(l.out, "%s %s\n", colorize("│", l.tagColor), line)
		}
	}
	fmt.Fprintln(l.out, footer)
}

// ---- Component loggers ------------------------------------------------------
//
// One named, colored logger per component. Add more here as Raven grows;
// keep each component's color distinct so terminal output stays scannable.

var (
	tgTransportLogger = New("TG Transport", blue, os.Stdout)
	tgSessionLogger   = New("TG Session", cyan, os.Stdout)
	agentLogger       = New("Agent", magenta, os.Stdout)
	geminiLogger      = New("Gemini", yellow, os.Stdout)
	remoteSSHLogger   = New("Remote SSH", green, os.Stdout)
	bouncerLogger     = New("Bouncer", red, os.Stdout)
)
