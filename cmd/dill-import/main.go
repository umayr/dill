// dill-import is an optional offline Compose-to-Pkl translator.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	importer "github.com/umayr/dill/internal/import"
)

const defaultSchema = "package://pkg.ushd.ca/dill@0.5.0#/dill.pkl"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "compose" {
		fmt.Fprintln(stderr, "usage: dill-import compose [--schema URI] FILE (use - for stdin)")
		return 2
	}
	fs := flag.NewFlagSet("compose", flag.ContinueOnError)
	fs.SetOutput(stderr)
	schema := fs.String("schema", defaultSchema, "Pkl schema amends URI or path")
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "compose requires exactly one input file (or - for stdin)")
		return 2
	}
	var reader io.Reader = stdin
	if fs.Arg(0) != "-" {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(stderr, "dill-import: cannot open input:", err)
			return 1
		}
		defer f.Close()
		reader = f
	}
	result, err := importer.Convert(reader, *schema)
	if err != nil {
		fmt.Fprintln(stderr, "dill-import:", err)
		return 2
	}
	n, err := stdout.Write(result)
	if err == nil && n != len(result) {
		err = io.ErrShortWrite
	}
	if err != nil {
		fmt.Fprintln(stderr, "dill-import: cannot write output:", err)
		return 1
	}
	return 0
}
