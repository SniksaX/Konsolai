package commands

import "context"

// InputMode is an alternate line mode for the REPL. While Active reports true, a
// line that does not start with "/" is handed to Handle instead of a turn.
type InputMode interface {
	Label() string
	Handle(ctx context.Context, env *Env, line string) error
	Active() bool
}

// Claimer is optional on an InputMode. A mode that also implements it is offered
// each slash line before the registry dispatches it; handled=true swallows the
// line, handled=false lets it dispatch as usual.
type Claimer interface {
	Claim(line string) (handled bool, err error)
}

type namedMode struct {
	name string
	mode InputMode
}

// inputModes are registered by build-tagged files; empty by default.
var inputModes []namedMode

// RegisterInputMode adds a mode under a name. Call it from an init.
func RegisterInputMode(name string, mode InputMode) {
	if name == "" || mode == nil {
		return
	}
	inputModes = append(inputModes, namedMode{name: name, mode: mode})
}

// ActiveInputMode returns the first active mode and its name, or ("", nil).
func ActiveInputMode() (string, InputMode) {
	for _, nm := range inputModes {
		if nm.mode.Active() {
			return nm.name, nm.mode
		}
	}
	return "", nil
}
