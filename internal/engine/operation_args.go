package engine

import (
	"fmt"
	"strings"
	"time"
)

// nodeOperation describes command-specific preparation so state inspection cannot accidentally initialize or receive variable flags.
type nodeOperation struct {
	name                         string
	readOnly, vars, backup, init bool
}

func classifyOperation(args []string) (nodeOperation, error) {
	if len(args) == 0 {
		return nodeOperation{}, fmt.Errorf("run needs a native command after --")
	}
	op := nodeOperation{name: args[0]}
	start, minArgs, maxArgs := 1, 0, 0
	switch args[0] {
	case "init":
		op.init = true
	case "console":
		op.readOnly, op.vars = true, true
	case "import":
		op.vars, op.backup = true, true
		minArgs, maxArgs = 2, 2
	case "state":
		if len(args) < 2 {
			return op, fmt.Errorf("run state needs list, show, pull, mv, or rm")
		}
		op.name += " " + args[1]
		start = 2
		switch args[1] {
		case "list":
			op.readOnly = true
			maxArgs = -1
		case "show":
			op.readOnly = true
			minArgs, maxArgs = 1, 1
		case "pull":
			op.readOnly = true
		case "mv":
			op.backup = true
			minArgs, maxArgs = 2, 2
		case "rm":
			op.backup = true
			minArgs, maxArgs = 1, -1
		default:
			return op, fmt.Errorf("unsupported state operation; use list, show, pull, or same-state mv/rm; cross-state operations require a separate native recovery procedure")
		}
	default:
		return op, fmt.Errorf("unsupported run operation; use terragraph plan/apply/destroy for graph changes, or run init, console, import, and supported state commands")
	}
	positional := 0
	for _, arg := range args[start:] {
		if !strings.HasPrefix(arg, "-") {
			positional++
			continue
		}
		key, value, hasValue := strings.Cut(arg, "=")
		switch {
		case key == "-no-color" && !hasValue:
		case key == "-lock-timeout" && hasValue && !op.init && (!op.readOnly || op.name == "console"):
			duration, err := time.ParseDuration(value)
			if err != nil || duration < 0 {
				return op, fmt.Errorf("-lock-timeout needs a nonnegative duration")
			}
		case key == "-id" && hasValue && op.name == "state list":
		default:
			return op, fmt.Errorf("run does not allow %s for this operation; keep backend, state, workspace, variable, backup, and locking configuration managed by the selected node", key)
		}
	}
	if positional < minArgs || (maxArgs >= 0 && positional > maxArgs) {
		return op, fmt.Errorf("run %s received the wrong number of positional arguments; use native addresses and IDs after the command", op.name)
	}
	return op, nil
}
