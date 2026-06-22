package raven

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed assets/policies/remoteSSH/defaultShellPolicy.yaml
var defaulShellPolicyYaml []byte
var defaultShellPolicy = func() *shellPolicy {
	policy, err := compileShellPolicy(bytes.NewBuffer(defaulShellPolicyYaml))
	if err != nil {
		panic(err)
	}
	return policy
}()

//go:embed assets/templates/remoteSSH/policy.tmpl
var shellPolicyTmplRaw string
var shellPolicyTmpl = func() *template.Template {
	funcMap := template.FuncMap{
		"ordinal": ordinal,
		"add": func(a, b int) int {
			return a + b
		},
		"oneline": func(s string) string {
			return strings.Join(strings.Fields(s), " ")
		},
	}

	return template.Must(
		template.
			New("shell security policy template").
			Funcs(funcMap).
			Parse(shellPolicyTmplRaw),
	)
}()

type sshBouncer struct {
	Policy *shellPolicy
}

func compileShellPolicy(f io.Reader) (*shellPolicy, error) {

	var policy shellPolicy
	if err := yaml.NewDecoder(f).Decode(&policy); err != nil {
		return nil, fmt.Errorf("ssh bouncer : policy parsing error : %w", err)
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
				return nil, fmt.Errorf("ssh bouncer: parse policy: command %s flag %s: %w", cmd.Name, cmd.Flags[i].Name, err)
			}
			cmd.Flags[i].ValueRegex = valueRegex
			cmd.FlagsMap[cmd.Flags[i].Name] = cmd.Flags[i]
		}

		for i, pos := range cmd.Positionals {

			for _, accept := range pos.AcceptPattern {

				regex, err := regexp.Compile(accept)
				if err != nil {
					return nil, fmt.Errorf("ssh bouncer: parse policy: command %s positional %d accept pattern %s: %w", cmd.Name, i+1, accept, err)
				}
				pos.AcceptPatternRegex = append(pos.AcceptPatternRegex, regex)
			}

			for _, reject := range pos.RejectPattern {
				regex, err := regexp.Compile(reject)
				if err != nil {
					return nil, fmt.Errorf("ssh bouncer: parse policy: command %s positional %d reject pattern %s: %w", cmd.Name, i+1, reject, err)
				}
				pos.RejectPatternRegex = append(pos.RejectPatternRegex, regex)
			}
		}

		policy.CommandsMap[cmd.Name] = cmd
	}
	return &policy, nil
}

func newSSHBouncer() (*sshBouncer, error) {

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("ssh bouncer: failed to get home dir: %w", err)
	}

	policyPath := filepath.Join(
		home,
		".raven",
		"policies",
		"remoteSSH",
		"shellPolicy.yaml",
	)

	policy := defaultShellPolicy

	if f, err := os.Open(policyPath); err == nil {

		defer f.Close()

		customPolicy, err := compileShellPolicy(f)
		if err != nil {
			return nil, fmt.Errorf(
				"ssh bouncer: invalid custom policy %s: %w",
				policyPath,
				err,
			)
		}

		policy = customPolicy

	} else if !errors.Is(err, os.ErrNotExist) {

		return nil, fmt.Errorf(
			"ssh bouncer: open custom policy %s: %w",
			policyPath,
			err,
		)
	}

	return &sshBouncer{
		Policy: policy,
	}, nil
}

func (b *sshBouncer) checkDenyList(val string) error {

	violationErr := fmt.Errorf("ssh bouncer: value %q is prohibited", val)

	cleaned := filepath.Clean(val)
	if slices.Contains(b.Policy.DenyList.Exact, cleaned) {
		return violationErr
	}

	for _, pattern := range b.Policy.DenyList.patternsRegex {
		if pattern.MatchString(cleaned) {
			return violationErr
		}
	}

	return nil
}

func (b *sshBouncer) validate(fc *remoteSSHFunctionCall) error {

	cmd, ok := b.Policy.CommandsMap[fc.Command]
	if !ok {
		return fmt.Errorf("ssh bouncer: command %q is not in policy", fc.Command)
	}

	// check flags
	for _, fcFlag := range fc.Flags {

		flag, ok := cmd.FlagsMap[fcFlag.Name]

		if !ok {
			return fmt.Errorf("ssh bouncer: flag %q is not allowed on command %q", fcFlag.Name, fc.Command)
		}

		if len(fcFlag.Value) == 0 && flag.TakesVal {
			return fmt.Errorf("ssh bouncer: flag %q requires a value on command %q", fcFlag.Name, fc.Command)
		}

		if flag.TakesVal {
			err := b.checkDenyList(fcFlag.Value)
			if err != nil {
				return err
			}
			if !flag.ValueRegex.MatchString(fcFlag.Value) {
				return fmt.Errorf("ssh bouncer: value %q is not allowed for flag %q on command %q", fcFlag.Value, fcFlag.Name, fc.Command)
			}
		}
	}

	// check positionals
	for i, fcPos := range fc.Positionals {

		err := b.checkDenyList(fcPos.Value)
		if err != nil {
			return err
		}

		if fcPos.Index < 0 || fcPos.Index > len(cmd.Positionals)-1 {
			err = fmt.Errorf("ssh bouncer: positional index %d is not defined for command %q", fcPos.Index, fc.Command)
			return err
		}

		// check for duplicate positionals
		for j := i - 1; j >= 0; j-- {
			if fc.Positionals[j].Index == fc.Positionals[i].Index {
				err = fmt.Errorf("ssh bouncer: positional index %d passed more than once on command %q", fc.Positionals[j].Index, fc.Command)
				return err
			}
		}

		posIdx := fcPos.Index

		if slices.Contains(cmd.Positionals[posIdx].RejectList, fcPos.Value) {
			return fmt.Errorf("ssh bouncer: value %q is prohibited at %s positional on command %q", fcPos.Value, ordinal(posIdx+1), fc.Command)
		}

		for j, pattern := range cmd.Positionals[posIdx].RejectPatternRegex {
			patternStr := cmd.Positionals[posIdx].RejectPattern[j]
			if pattern.MatchString(fcPos.Value) {
				return fmt.Errorf("ssh bouncer: value matching pattern %q is prohibited at %s positional on command %q", patternStr, ordinal(posIdx+1), fc.Command)
			}
		}

		if len(cmd.Positionals[posIdx].AcceptPattern) == 0 {
			continue
		}

		violationErr := fmt.Errorf("ssh bouncer: value %q does not match any accepted pattern at positional %d on command %q", fcPos.Value, fcPos.Index, fc.Command)
		matched := false
		for _, reg := range cmd.Positionals[posIdx].AcceptPatternRegex {
			matched = matched || reg.MatchString(fcPos.Value)
		}

		if !matched {
			return violationErr
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
				err := fmt.Errorf("ssh bouncer: positional %d is required on command %q but not provided", pos.Index, fc.Command)
				return err
			}
		}
	}

	return nil
}

func (b *sshBouncer) describe(toolName llmToolName) (string, error) {
	var buf bytes.Buffer
	payload := &struct {
		*sshBouncer
		ToolName llmToolName
	}{
		sshBouncer: b,
		ToolName:   toolName,
	}

	err := shellPolicyTmpl.Execute(&buf, &payload)
	if err != nil {
		return "", fmt.Errorf("ssh bouncer: execute policy template: %w", err)
	}

	return buf.String(), nil
}

// TODO:Parse default policy command templates
func (b *sshBouncer) constructCmd(cmd *remoteSSHFunctionCall) (string, error) {

	dataMap := make(map[string]string)

	for _, pos := range cmd.Positionals {
		dataMap[strconv.Itoa(pos.Index)] = shellQuote(pos.Value)
	}

	for _, f := range cmd.Flags {

		if b.Policy.CommandsMap[cmd.Command].FlagsMap[f.Name].TakesVal {
			if b.Policy.CommandsMap[cmd.Command].FlagsMap[f.Name].Glued {
				dataMap[f.Name] = f.Name + "=" + shellQuote(f.Value)
			} else {
				dataMap[f.Name] = f.Name + " " + shellQuote(f.Value)
			}
		} else {
			dataMap[f.Name] = f.Name
		}
	}

	cmdTempl := b.Policy.CommandsMap[cmd.Command].Template

	funcMap := template.FuncMap{
		"flag": func(flagStr string) string {
			var parts []string
			for _, f := range strings.Fields(flagStr) {
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
		return "", fmt.Errorf("ssh bouncer : parse command %s template : %w", cmd.Command, err)
	}

	var buf bytes.Buffer

	if err := templ.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("ssh bouncer : execute command %s template : %w", cmd.Command, err)
	}

	return strings.TrimSpace(buf.String()), nil
}
