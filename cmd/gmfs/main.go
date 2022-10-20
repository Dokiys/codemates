package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"syscall"

	"github.com/Dokiys/codemates/gmfs"
)

var s = flag.String("s", ".*", "Regexp match struct name.")

func usage() {
	_, _ = fmt.Fprintf(os.Stderr, "usage: gstm [-s] GO_FILES\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flag.Parse()

	exp, err := regexp.Compile(*s)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "-s invalid: %s\n", err)
		usage()
	}

	if len(flag.Args()) <= 0 {
		usage()
	}
	for _, src := range flag.Args() {
		f, err := os.Open(src)
		if err != nil {
			if errors.Is(err, syscall.ENOENT) {
				continue
			}

			errExit(err)
		}

		w := os.Stdout
		if err := gmfs.GenMsg(f, w, *exp); err != nil {
			errExit(err)
		}
	}

	return
}

func errExit(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
