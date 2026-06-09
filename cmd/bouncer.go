package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/pelletier/go-toml/v2"
)

type bouncer struct {
	Policy    *shellPolicy
	errPrefix string
}

func newBouncer() (*bouncer, error) {

	f, err := os.Open("SSHPolicy.toml")
	if err != nil {
		return nil, fmt.Errorf("could not open SSHPolicy file : %v", err)
	}

	var policy shellPolicy
	if err := toml.NewDecoder(f).Decode(&policy); err != nil {
		return nil, fmt.Errorf("policy parsing error : %v", err)
	}

	for _, pattern := range policy.DenyList.Patterns {
		reg, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		policy.DenyList.patternsRegex = append(policy.DenyList.patternsRegex, reg)
	}

	policy.CommandsMap = make(map[string]*shellCommand)

	for _, cmd := range policy.Commands {

		flagsMap := make(map[string]*shellFlag)

		cmd.FlagsMap = flagsMap

		for i := range cmd.Flags {
			if !cmd.Flags[i].TakesVal {
				cmd.FlagsMap[cmd.Flags[i].Name] = cmd.Flags[i]
				continue
			}
			valueRegex, err := regexp.Compile(cmd.Flags[i].ValuePattern)
			if err != nil {
				return nil, fmt.Errorf("command %s flag %s value regex parsing error : %v", cmd.Name, cmd.Flags[i].Name, err)
			}
			cmd.Flags[i].ValueRegex = valueRegex
			cmd.FlagsMap[cmd.Flags[i].Name] = cmd.Flags[i]
		}

		for i, pos := range cmd.Positionals {

			for _, accept := range pos.AcceptPattern {

				regex, err := regexp.Compile(accept)
				if err != nil {
					return nil, fmt.Errorf("command %s positional %d accept regex %s parsing error : %v", cmd.Name, i, accept, err)
				}
				pos.AcceptPatternRegex = append(pos.AcceptPatternRegex, regex)
			}

			for _, reject := range pos.RejectPattern {
				regex, err := regexp.Compile(reject)
				if err != nil {
					return nil, fmt.Errorf("command %s positional %d reject regex %s parsing error : %v", cmd.Name, i, reject, err)
				}
				pos.RejectPatternRegex = append(pos.RejectPatternRegex, regex)
			}
		}

		policy.CommandsMap[cmd.Name] = cmd
	}

	return &bouncer{
		Policy:    &policy,
		errPrefix: "validate command error :",
	}, nil
}

func (b *bouncer) ordinal(n int) string {
	switch n % 100 {
	case 11, 12, 13:
		return fmt.Sprintf("%dth", n)
	default:
		switch n % 10 {
		case 1:
			return fmt.Sprintf("%dst", n)
		case 2:
			return fmt.Sprintf("%dnd", n)
		case 3:
			return fmt.Sprintf("%drd", n)
		default:
			return fmt.Sprintf("%dth", n)
		}
	}
}

func (b *bouncer) checkDenyList(val string) error {

	violationErr := fmt.Errorf("%s %s can't be accepted as command argument/value", b.errPrefix, val)

	if slices.Contains(b.Policy.DenyList.Exact, val) {
		return violationErr
	}

	for _, pattern := range b.Policy.DenyList.patternsRegex {
		if pattern.MatchString(val) {
			return violationErr
		}
	}

	return nil
}

func (b *bouncer) validate(fc *remoteSSHFunctionCall) error {

	cmd, ok := b.Policy.CommandsMap[fc.Command]
	if !ok {
		return fmt.Errorf("%s command not allowed", b.errPrefix)
	}

	// check flags
	for _, fcFlag := range fc.Flags {

		flag, ok := cmd.FlagsMap[fcFlag.Name]

		if !ok {
			return fmt.Errorf("%s flag %s not allowed on command %s", b.errPrefix, fcFlag.Name, fc.Command)
		}

		if len(fcFlag.Value) == 0 && flag.TakesVal {
			return fmt.Errorf("%s flag %s value not provided on command %s", b.errPrefix, fcFlag.Name, fc.Command)
		}

		if flag.TakesVal {
			err := b.checkDenyList(fcFlag.Value)
			if err != nil {
				return err
			}
			if !flag.ValueRegex.MatchString(fcFlag.Value) {
				return fmt.Errorf("%s %s value not allowed on %s flag on command %s", b.errPrefix, fcFlag.Value, fcFlag.Name, fc.Command)
			}
		}
	}

	// check positionals
	for i, fcPos := range fc.Positionals {

		err := b.checkDenyList(fcPos.Value)
		if err != nil {
			return err
		}

		if fcPos.Index < 1 || fcPos.Index > len(cmd.Positionals) {
			err = fmt.Errorf("%s positional with index %d not defined in command %s according to shell security policy", b.errPrefix, fcPos.Index, fc.Command)
			return err
		}

		// check for duplicate positionals
		for j := i - 1; j >= 0; j-- {
			if fc.Positionals[j].Index == fc.Positionals[i].Index {
				err = fmt.Errorf("%s positional with index %d passed more than once on command %s", b.errPrefix, fc.Positionals[j].Index, fc.Command)
				return err
			}
		}

		posIdx := fcPos.Index - 1

		if slices.Contains(cmd.Positionals[posIdx].RejectList, fcPos.Value) {
			return fmt.Errorf("%s %s positional value at %s positional not allowed for command %s", b.errPrefix, fcPos.Value, b.ordinal(posIdx+1), fc.Command)
		}

		for j, pattern := range cmd.Positionals[posIdx].RejectPatternRegex {
			patternStr := cmd.Positionals[posIdx].RejectPattern[j]
			if pattern.MatchString(fcPos.Value) {
				return fmt.Errorf("%s positional value satisfying regex pattern %s at %s positional not allowed for command %s", b.errPrefix, patternStr, b.ordinal(posIdx+1), fc.Command)
			}
		}

		if len(cmd.Positionals[posIdx].AcceptPattern) == 0 {
			continue
		}

		violationErr := fmt.Sprintf("%s positional value %s on positional argument with index %d violates command policy of command %s", b.errPrefix, fcPos.Value, fcPos.Index, fc.Command)
		matched := false
		for _, reg := range cmd.Positionals[posIdx].AcceptPatternRegex {
			matched = matched || reg.MatchString(fcPos.Value)
		}

		if !matched {
			return fmt.Errorf("%s", violationErr)
		}
	}

	// check for missing positional args
	for _, pos := range cmd.Positionals {
		if pos.Required {
			found := false
			for _, fcPos := range fc.Positionals {
				if pos.Index == fcPos.Index {
					found = true
				}
			}
			if !found {
				err := fmt.Errorf("%s positional with index %d is required on command %s but not provided", b.errPrefix, pos.Index, fc.Command)
				return err
			}
		}
	}

	return nil
}

func (b *bouncer) describe() (string, error) {

	shellPolicyTempl := `
	This is shell command security policy.
	Shell commands you wish to run using execute_ssh tool must strictly adhere to this shell security policy.

	- Avoid long hanging commands.
	- Variadic positional arguments are strictly not allowed.
	- If required split the shell prompts into multiple prompts to satisfy the policy

	**List of globally prohibited exact values:**
	Flag values and positional arguments should not be equal to any of the elements of this list.

	Here is the list:

	{{range .Policy.DenyList.Exact}}
	- {{.}}
	{{end}}

	**List of globally prohibited regular expression patterns for values:**
	Flag values and positional arguments should not satisfy any of these regular expressions in the list.

	Here is the list:

	{{range .Policy.DenyList.Patterns}}
	- {{.}}
	{{end}}

	**Per command policy which must be adhered to while using the command :

	{{range .Policy.Commands}}
	Command Name : {{.Name}}
	Allowed intended use : {{.Description}}
	{{if gt (len .Positionals) 0}}
	Allowed list of positionals on command :

		{{range $i, $pos := .Positionals}}
		Command accepts {{ordinal (add $i 1)}} positional arg
		Positional argument index : {{$pos.Index}}
		Required : {{if $pos.Required}} Yes {{else}} No {{end}}
		{{if gt (len $pos.RejectList) 0}}
		For this positional the following values are prohibited literally:
		{{range $pos.RejectList}}
		- {{.}}
		{{end}}
		{{end}}
		{{if gt (len $pos.RejectPattern) 0}}
		For this positional, values satisfying the following regular expressions are prohibited:
		{{range $pos.RejectPattern}}
		- {{.}}
		{{end}}
		{{end}}
		{{if gt (len $pos.AcceptPattern) 0}}
		For this positional, values satisfying the following regular expressions are accepted; reject patterns take precedence over accept patterns:
		{{range $pos.AcceptPattern}}
		- {{.}}
		{{end}}
		{{end}}
		{{end}}

	{{end}}
	{{if gt (len .Flags) 0}}
	Allowed flags on the command :

		{{range .Flags}}
		Flag name : {{.Name}}
		{{if .TakesVal}}
		Allowed flag value regex pattern : {{.ValuePattern}}
	 	{{end}}
		{{end}}
	{{end}}
	{{end}}
	`

	templ := template.New("shell security policy template").Funcs(template.FuncMap{
		"ordinal": b.ordinal,
		"add": func(a, b int) int {
			return a + b
		},
	})

	parsedTempl, err := templ.Parse(shellPolicyTempl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = parsedTempl.Execute(&buf, b)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (b *bouncer) constructCmd(cmd *remoteSSHFunctionCall) (string, error) {

	dataMap := make(map[string]string)

	for _, pos := range cmd.Positionals {
		dataMap[strconv.Itoa(pos.Index)] = pos.Value
	}

	for _, f := range cmd.Flags {

		if b.Policy.CommandsMap[cmd.Command].FlagsMap[f.Name].TakesVal {
			if b.Policy.CommandsMap[cmd.Command].FlagsMap[f.Name].Glued {
				dataMap[f.Name] = f.Name + "=" + f.Value
			} else {
				dataMap[f.Name] = f.Name + " " + f.Value
			}
		} else {
			dataMap[f.Name] = f.Name
		}
	}

	cmdTempl := b.Policy.CommandsMap[cmd.Command].Template

	funcMap := template.FuncMap{
		"flag": func(flags ...string) string {
			var parts []string
			for _, f := range flags {
				if val, ok := dataMap[f]; ok {
					parts = append(parts, val)
				}
			}
			return strings.Join(parts, " ")
		},

		"pos": func(positions ...int) string {
			var parts []string
			for _, p := range positions {
				if val, ok := dataMap[strconv.Itoa(p)]; ok {
					parts = append(parts, val)
				}
			}
			return strings.Join(parts, " ")
		},
	}

	templ, err := template.New("command template").Funcs(funcMap).Parse(cmdTempl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer

	if err := templ.Execute(&buf, nil); err != nil {
		return "", err
	}

	return strings.TrimSpace(buf.String()), nil
}
