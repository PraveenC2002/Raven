package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type bouncer struct {
	policy    *shellPolicy
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

	for _, pattern := range policy.DenyList.Exact {
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
			valueRegex, err := regexp.Compile(cmd.Flags[i].Value)
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
		policy:    &policy,
		errPrefix: "validate command error :",
	}, nil
}

func (b *bouncer) checkDenyList(val string) error {

	violationErr := fmt.Errorf("%s %s can't be accepted as command argument/value", b.errPrefix, val)

	if slices.Contains(b.policy.DenyList.Exact, val) {
		return violationErr
	}

	for _, pattern := range b.policy.DenyList.patternsRegex {
		if pattern.MatchString(val) {
			return violationErr
		}
	}

	return nil
}

func (b *bouncer) inRange(i, n int) bool {
	return i < n
}

func (b *bouncer) isFlag(token string) bool {
	return token[0] == '-'
}

func (b *bouncer) isFlagGlued(token string) int {
	return strings.Index(token, "=")
}

func (b *bouncer) validate(cmd string) error {

	if len(cmd) == 0 {
		return fmt.Errorf("%s empty command string", b.errPrefix)
	}

	tokens := strings.Fields(cmd)

	command, ok := b.policy.CommandsMap[tokens[0]]
	if !ok {
		return fmt.Errorf("%s command not allowed", b.errPrefix)
	}

	n := len(tokens)

	i := 1

	// cmd has flags
	if len(command.Flags) != 0 {

		for b.inRange(i, n) && b.isFlag(tokens[i]) {

			token := tokens[i]
			var flToken string
			isGlued := b.isFlagGlued(tokens[i])

			if isGlued != -1 {
				flToken = token[:isGlued+1]
			} else {
				flToken = token
			}

			flag, ok := command.FlagsMap[flToken]
			if !ok {
				return fmt.Errorf("%s flag %s not allowed on command %s", b.errPrefix, flToken, command.Name)
			}

			if !flag.TakesVal && isGlued != -1 {
				return fmt.Errorf("%s flag value not allowed on %s flag in %s command ", b.errPrefix, flToken, command.Name)
			}

			if flag.TakesVal {

				if (isGlued != -1 && len(token)-1 == isGlued) || (isGlued == -1 && !b.inRange(i+1, n)) {
					return fmt.Errorf("%s flag value not provided on %s flag in %s command ", b.errPrefix, flToken, command.Name)
				}

				var flValue string

				if isGlued != -1 {
					flValue = token[isGlued+1:]
				} else {
					flValue = tokens[i+1]
				}

				if err := b.checkDenyList(flValue); err != nil {
					return fmt.Errorf("%s \n command : %s", err.Error(), command.Name)
				}

				ok := flag.ValueRegex.MatchString(flValue)
				if !ok {
					err := fmt.Sprintf("%s value %s not allowed on flag %s in command %s", b.errPrefix, tokens[i], tokens[i-1], command.Name)
					return fmt.Errorf("%s", err)
				}

				if isGlued == -1 {
					i += 2
				} else {
					i++
				}
			} else {
				i++
			}
		}
	}

	// cmd has positionals
	for posIdx, pos := range command.Positionals {

		if !pos.Required && !b.inRange(i, n) {
			break
		}

		if pos.Required && !b.inRange(i, n) {
			return fmt.Errorf("%s %dth required positional argument on %s command not found", b.errPrefix, posIdx+1, command.Name)
		}

		arg := tokens[i]

		violationErr := fmt.Sprintf("%s %dth positional argument %s violates command policy on command %s", b.errPrefix, posIdx+1, arg, command.Name)

		for _, str := range pos.RejectList {
			if arg == str {
				return fmt.Errorf("%s", violationErr)
			}
		}

		for _, reg := range pos.RejectPatternRegex {
			if reg.MatchString(arg) {
				return fmt.Errorf("%s", violationErr)
			}
		}

		matched := false
		for _, reg := range pos.AcceptPatternRegex {
			matched = matched || reg.MatchString(arg)
		}

		if !matched {
			return fmt.Errorf("%s", violationErr)
		}

		i++
	}

	if i != len(tokens) {
		return fmt.Errorf("%s invalid command, leftover %s", b.errPrefix, strings.Join(tokens[i:], ""))
	}

	return nil
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

func (b *bouncer) describe() string {
	var sb strings.Builder
	sb.WriteString("This is shell command security policy.\n" +
		"Commands given must strictly adhere to this policy.\n" +
		"Also avoid long hanging commands.\n" +
		"Every command must match the following command structure :\n" +
		"command [flags...] [positional arguments...]\n" +
		"All flags must strictly come before positional arguments.\n" +
		"All positional arguments must strictly come after flags.\n" +
		"Variadic positional arguments are strictly not allowed.\n"+ 
		"If required split the shell prompts into multiple prompts to satisfy the policy\n",
	)

	sb.WriteString("\n---\n\nList of globally prohibited exact values:\n")
	sb.WriteString("Flag values and positional arguments should not be equal to any of the elements of this list.\nHere is the list:\n\n")
	for _, exact := range b.policy.DenyList.Exact {
		sb.WriteString(exact)
		sb.WriteByte('\n')
	}

	sb.WriteString("\n---\n\nList of globally prohibited regular expression patterns for values:\n")
	sb.WriteString("Flag values and positional arguments should not satisfy any of these regular expressions in the list.\nHere is the list:\n\n")
	for _, patt := range b.policy.DenyList.Patterns {
		sb.WriteString(patt)
		sb.WriteByte('\n')
	}

	sb.WriteString("\n---\n\nCommand policy which must be adhered to:\n")
	for _, cmd := range b.policy.Commands {
		sb.WriteString("\n---\n\n")
		sb.WriteString("Command name: ")
		sb.WriteString(cmd.Name)
		sb.WriteByte('\n')
		sb.WriteString("Command description: ")
		sb.WriteString(cmd.Description)
		sb.WriteByte('\n')
		for _, f := range cmd.Flags {
			sb.WriteString("Command accepts flag: ")
			sb.WriteString(f.Name)
			sb.WriteByte('\n')
			if f.TakesVal {
				sb.WriteString("Flag ")
				sb.WriteString(f.Name)
				sb.WriteString(" takes value satisfying regex: ")
				sb.WriteString(f.Value)
				sb.WriteByte('\n')
			}
		}
		for i, pos := range cmd.Positionals {
			sb.WriteString("Command accepts ")
			sb.WriteString(b.ordinal(i + 1))
			sb.WriteString(" positional arg\n")
			if pos.Required {
				sb.WriteString("This positional arg is required\n")
			} else {
				sb.WriteString("This positional is optional\n")
			}
			if len(pos.RejectList) != 0 {
				sb.WriteString("For this positional the following values are prohibited literally:\n")
				for _, rej := range pos.RejectList {
					sb.WriteString(rej)
					sb.WriteByte('\n')
				}
			}
			if len(pos.RejectPattern) != 0 {
				sb.WriteString("For this positional, values satisfying the following regular expressions are prohibited:\n")
				for _, rej := range pos.RejectPattern {
					sb.WriteString(rej)
					sb.WriteByte('\n')
				}
			}
			if len(pos.AcceptPattern) != 0 {
				sb.WriteString("For this positional, values satisfying the following regular expressions are accepted; reject patterns take precedence over accept patterns:\n")
				for _, acpt := range pos.AcceptPattern {
					sb.WriteString(acpt)
					sb.WriteByte('\n')
				}
			}
		}
	}
	sb.WriteString("\n---\n")
	return sb.String()
}

