package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiDim    = "\033[2m"
)

// Render writes the text plan and returns the first write error, if any.
func Render(p *Plan, w io.Writer) error {
	isTTY := isTerminal(w)
	output := &planTextWriter{dest: w}
	w = output

	colour := func(s, code string) string {
		if !isTTY {
			return s
		}
		return code + s + ansiReset
	}

	var creates, recreates, removes, noops int

	for _, ch := range p.Changes {
		switch ch.Kind {
		case KindCreate:
			creates++
			fmt.Fprintf(w, "  %s %-20s will be created\n",
				colour("+", ansiGreen), ch.Service)
		case KindRecreate:
			recreates++
			fmt.Fprintf(w, "  %s %-20s will be recreated\n",
				colour("~", ansiYellow), ch.Service)
			fieldWidth := 16
			for _, d := range ch.Diffs {
				fieldWidth = max(fieldWidth, utf8.RuneCountInString(d.Field))
			}
			for _, d := range ch.Diffs {
				renderDiff(w, d, fieldWidth, colour)
			}
		case KindRemove:
			removes++
			fmt.Fprintf(w, "  %s %-20s will be removed\n",
				colour("-", ansiRed), ch.Service)
		case KindNoop:
			noops++
			fmt.Fprintf(w, "  %s %-20s no changes\n",
				colour("=", ansiDim), ch.Service)
		}
	}

	for _, step := range p.Hooks {
		fmt.Fprintf(w, "  hook %s %s: %s\n", step.Phase, step.Job, strings.Join(step.Services, ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Plan: %d to create, %d to recreate, %d to remove, %d unchanged.\n",
		creates, recreates, removes, noops)
	return output.err
}

// Preserve the existing write sequence for callers that render a plan as
// best-effort output, while allowing read-only commands to report failure.
type planTextWriter struct {
	dest io.Writer
	err  error
}

func (w *planTextWriter) Write(p []byte) (int, error) {
	n, err := w.dest.Write(p)
	if w.err == nil {
		w.err = err
	}
	return n, err
}

func renderDiff(w io.Writer, d FieldDiff, fieldWidth int, colour func(string, string) string) {
	const indent = "        "
	field := fmt.Sprintf("%-*s", fieldWidth, d.Field)
	// Keep multiline values on their own diff row, without changing the plan.
	if strings.ContainsAny(d.Before, "\r\n") {
		d.Before = strconv.Quote(d.Before)
	}
	if strings.ContainsAny(d.After, "\r\n") {
		d.After = strconv.Quote(d.After)
	}

	switch {
	case d.Before == "" && d.After != "":
		// addition
		fmt.Fprintf(w, "%s%s %s\n", indent, colour("+", ansiGreen), field+" "+d.After)
	case d.Before != "" && d.After == "":
		// removal
		fmt.Fprintf(w, "%s%s %s\n", indent, colour("-", ansiRed), field+" "+d.Before)
	default:
		// change
		fmt.Fprintf(w, "%s  %s %s → %s\n", indent, field,
			colour(d.Before, ansiRed), colour(d.After, ansiGreen))
	}
}

// RenderJSON writes the plan as a JSON object with a "changes" array and a
// "summary" breakdown, suitable for machine consumption.
func RenderJSON(p *Plan, w io.Writer) error {
	type summary struct {
		Create   int `json:"create"`
		Recreate int `json:"recreate"`
		Remove   int `json:"remove"`
		Noop     int `json:"noop"`
	}
	type output struct {
		Hooks   []HookStep `json:"hooks,omitempty"`
		Changes []Change   `json:"changes"`
		Summary summary    `json:"summary"`
	}

	var s summary
	for _, ch := range p.Changes {
		switch ch.Kind {
		case KindCreate:
			s.Create++
		case KindRecreate:
			s.Recreate++
		case KindRemove:
			s.Remove++
		case KindNoop:
			s.Noop++
		}
	}

	changes := p.Changes
	if changes == nil {
		changes = []Change{}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(output{Changes: changes, Summary: s, Hooks: p.Hooks})
}

func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}
