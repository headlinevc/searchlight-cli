package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type Mode struct {
	Pretty bool
	Quiet  bool
}

func IsTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// WriteJSON writes raw to stdout, optionally pretty-printed. Raw must already
// be valid JSON (we don't validate — that's the caller's responsibility).
func WriteJSON(w io.Writer, raw json.RawMessage, pretty bool) error {
	if !pretty {
		_, err := w.Write(append([]byte(raw), '\n'))
		return err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Not parseable as JSON — fall back to writing it raw so the user can debug.
		_, werr := w.Write(append([]byte(raw), '\n'))
		return werr
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// WriteValue marshals v and writes it to w (pretty if requested).
func WriteValue(w io.Writer, v any, pretty bool) error {
	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

// HumanF prints to stderr unless quiet is set. Use only for progress messages,
// hints, and other context the agent doesn't need.
func HumanF(quiet bool, format string, a ...any) {
	if quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}
