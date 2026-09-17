package secrets

import "context"

// PromptSource asks the person. It is the last link of the chain, so empty
// input is an error: there is nowhere else to go.
type PromptSource struct {
	Prompt func(ctx context.Context, label string) (string, error)
	Label  string
}

func (p PromptSource) Password(ctx context.Context) (Secret, bool, error) {
	prompt := p.Prompt
	if prompt == nil {
		prompt = ReadPassword
	}
	label := p.Label
	if label == "" {
		label = "Password: "
	}
	v, err := prompt(ctx, label)
	if err != nil {
		return Secret{}, false, err
	}
	if v == "" {
		return Secret{}, false, errEmptyPassword
	}
	return New(v), true, nil
}
