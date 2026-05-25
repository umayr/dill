package logger

import (
	"os"
	"regexp"
	"strconv"

	charm "github.com/charmbracelet/log"
	"github.com/mattn/go-isatty"
)

var l = charm.New(os.Stderr)

var (
	rVerboseFlag    = regexp.MustCompile(`^-v+$`)
	rVerboseNumFlag = regexp.MustCompile(`^-v=(\d+)$`)
)

// ParseVerbosity scans args for -v, -vv, -vvv, -vvvv, -v=N flags.
// It returns the total verbosity count and the remaining args with those flags removed.
func ParseVerbosity(args []string) (int, []string) {
	count := 0
	remaining := args[:0:0]
	for _, arg := range args {
		if rVerboseFlag.MatchString(arg) {
			count += len(arg) - 1 // subtract leading '-'
			continue
		}
		if m := rVerboseNumFlag.FindStringSubmatch(arg); m != nil {
			n, _ := strconv.Atoi(m[1])
			count += n
			continue
		}
		remaining = append(remaining, arg)
	}
	return count, remaining
}

// Init configures the global logger based on verbosity level:
//
//	0, TTY     → Info   (default: useful output in the terminal)
//	0, no TTY  → Warn   (default: quiet when piped or scripted)
//	1 (-v)     → Debug
//	2 (-vv)    → Debug + caller
//	3+ (-vvv)  → Debug + caller + timestamps
func Init(verbosity int) {
	switch {
	case verbosity <= 0:
		if isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd()) {
			l.SetLevel(charm.InfoLevel)
		} else {
			l.SetLevel(charm.WarnLevel)
		}
	case verbosity == 1:
		l.SetLevel(charm.DebugLevel)
	case verbosity == 2:
		l.SetLevel(charm.DebugLevel)
		l.SetReportCaller(true)
	default:
		l.SetLevel(charm.DebugLevel)
		l.SetReportCaller(true)
		l.SetReportTimestamp(true)
	}
}

func Debug(msg string, args ...any) { l.Debug(msg, args...) }
func Info(msg string, args ...any)  { l.Info(msg, args...) }
func Warn(msg string, args ...any)  { l.Warn(msg, args...) }
func Error(msg string, args ...any) { l.Error(msg, args...) }
