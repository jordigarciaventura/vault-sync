package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/huh/v2"
)

func (p *prompter) form(ctx context.Context, field huh.Field) error {
	err := huh.NewForm(huh.NewGroup(field)).WithInput(p.terminalInput).WithOutput(p.out).RunWithContext(ctx)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) || ctx.Err() != nil {
			return errors.New("init cancelled; nothing was written")
		}
		return fmt.Errorf("show init form: %w", err)
	}
	return nil
}

func (p *prompter) selectBackend(ctx context.Context, types []string, def string) (string, error) {
	if p.terminal {
		answer := def
		options := make([]huh.Option[string], 0, len(types))
		for _, typ := range types {
			options = append(options, huh.NewOption(typ, typ))
		}
		err := p.form(ctx, huh.NewSelect[string]().Title("Backend").Options(options...).Value(&answer))
		return answer, err
	}
	_, _ = fmt.Fprintln(p.out, "\nBackends:")
	for i, typ := range types {
		_, _ = fmt.Fprintf(p.out, "  %d) %s\n", i+1, typ)
	}
	for {
		answer, err := p.ask(ctx, "Backend", def)
		if err != nil {
			return "", err
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(types) {
			return types[n-1], nil
		}
		if slices.Contains(types, strings.ToLower(answer)) {
			return strings.ToLower(answer), nil
		}
		p.warn("unknown backend %q; enter a number or one of: %s", answer, strings.Join(types, ", "))
	}
}

func (p *prompter) selectFiles(ctx context.Context, paths []string) ([]string, error) {
	if p.terminal {
		var selected []string
		options := make([]huh.Option[string], 0, len(paths))
		for _, path := range paths {
			options = append(options, huh.NewOption(path, path))
		}
		err := p.form(ctx, huh.NewMultiSelect[string]().Title("Use discovered environment files?").Description("Space toggles a file; Enter confirms. Leave all unchecked to skip.").Options(options...).Height(len(paths)+4).Value(&selected))
		return selected, err
	}
	_, _ = fmt.Fprintln(p.out, "\nDiscovered environment files:")
	for i, path := range paths {
		_, _ = fmt.Fprintf(p.out, "  %d) %s\n", i+1, path)
	}
	for {
		answer, err := p.ask(ctx, "Files to use (comma-separated numbers; empty skips)", "")
		if err != nil {
			return nil, err
		}
		if answer == "" {
			return nil, nil
		}
		var selected []string
		valid := true
		for _, token := range strings.Split(answer, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(token))
			if err != nil || n < 1 || n > len(paths) {
				valid = false
				break
			}
			if !slices.Contains(selected, paths[n-1]) {
				selected = append(selected, paths[n-1])
			}
		}
		if valid {
			return selected, nil
		}
		p.warn("choose numbers from 1 to %d, or leave empty to skip", len(paths))
	}
}

// askDefault keeps the built-in choice available alongside the last answer.
// Manual secret paths deliberately use ask instead, with no default.
func (p *prompter) askDefault(ctx context.Context, label, builtin, latest string) (string, error) {
	values := []string{builtin}
	selected := 0
	if latest != "" && latest != builtin {
		values = append(values, latest)
		selected = 1
	}
	if !p.terminal {
		for i, value := range values {
			text := value
			if text == "" {
				text = "(empty)"
			}
			_, _ = fmt.Fprintf(p.out, "  %d) %s\n", i+1, text)
		}
		return p.ask(ctx, label, values[selected])
	}
	options := make([]huh.Option[int], 0, len(values)+1)
	for i, value := range values {
		text := value
		if text == "" {
			text = "(empty)"
		}
		options = append(options, huh.NewOption(text, i))
	}
	options = append(options, huh.NewOption("Enter another value…", len(values)))
	if err := p.form(ctx, huh.NewSelect[int]().Title(label).Options(options...).Value(&selected)); err != nil {
		return "", err
	}
	if selected < len(values) {
		return values[selected], nil
	}
	return p.ask(ctx, label, "")
}
