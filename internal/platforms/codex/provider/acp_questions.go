package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	acp "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type acpQuestionField struct {
	question codexappserver.InputQuestion
	values   map[string]any
}

func (a *acpExecution) question(ctx context.Context, req acpexec.ElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	cancelled := acp.UnstableCreateElicitationResponse{Cancel: &acp.UnstableCreateElicitationCancel{}}
	a.mu.Lock()
	active := a.turns[string(req.SessionID)] != nil
	a.mu.Unlock()
	if !active || req.Form == nil {
		return cancelled, nil
	}
	fields, err := acpQuestionFields(req.Form.RequestedSchema)
	if err != nil {
		return cancelled, err
	}
	questions := make([]codexappserver.InputQuestion, len(fields))
	for i, field := range fields {
		questions[i] = field.question
	}
	id := "que_" + uuid.NewString()
	props := map[string]any{"id": id, "sessionID": string(req.SessionID), "questions": v1Questions(questions),
		"tool": map[string]any{"callID": req.ToolCallID}}
	responses := make(chan acp.UnstableCreateElicitationResponse, 1)
	pr := &prompt{session: string(req.SessionID), question: true, questions: questions, props: props, recorded: make(chan struct{})}
	pr.deliver = func(value any) error {
		input, ok := value.(codexappserver.UserInputResponse)
		if !ok {
			return errors.New("invalid ACP question response")
		}
		result := cancelled
		if len(input.Answers) > 0 {
			content := map[string]any{}
			for _, field := range fields {
				answers := input.Answers[field.question.ID].Answers
				if len(answers) == 0 {
					continue
				}
				if len(answers) != 1 {
					return errors.New("ACP scalar question needs one answer")
				}
				text := answers[0]
				v, known := field.values[text]
				if !known {
					if !field.question.IsOther && len(field.values) > 0 {
						return errors.New("ACP question answer was not offered")
					}
					v = text
				}
				content[field.question.ID] = v
			}
			raw, _ := json.Marshal(map[string]any{"action": "accept", "content": content})
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
		}
		select {
		case responses <- result:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	data, _ := json.Marshal(props)
	if err := a.b.sup.Publish(providerprotocol.Event{Type: "question.asked", Properties: data}, func() {
		a.b.mu.Lock()
		a.b.prompts[id] = pr
		a.b.mu.Unlock()
	}); err != nil {
		return cancelled, nil
	}
	defer a.b.cancelPrompt(id)
	select {
	case result := <-responses:
		select {
		case <-pr.recorded:
			return result, nil
		case <-ctx.Done():
		}
	case <-ctx.Done():
	}
	return cancelled, nil
}

// Only expose form types the existing question UI can represent faithfully.
// Unsupported/secret schemas fail closed instead of guessing or collecting secrets.
func acpQuestionFields(schema acp.UnstableElicitationSchema) ([]acpQuestionField, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var object struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(object.Properties))
	for key := range object.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]acpQuestionField, 0, len(keys))
	for _, key := range keys {
		var property struct {
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Enum        []any  `json:"enum"`
			OneOf       []struct {
				Const       any    `json:"const"`
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"oneOf"`
			Meta struct {
				Codex struct {
					IsSecret bool `json:"isSecret"`
					IsOther  bool `json:"isOther"`
				} `json:"codex"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(object.Properties[key], &property); err != nil {
			return nil, err
		}
		if property.Meta.Codex.IsSecret || (property.Type != "string" && property.Type != "boolean") {
			return nil, fmt.Errorf("ACP form field %q: %w", key, platforms.ErrUnsupported)
		}
		field := acpQuestionField{question: codexappserver.InputQuestion{ID: key, Question: property.Title, Header: property.Description, IsOther: property.Meta.Codex.IsOther}, values: map[string]any{}}
		if field.question.Question == "" {
			field.question.Question = key
		}
		add := func(label, description string, value any) {
			field.values[label] = value
			field.question.Options = append(field.question.Options, codexappserver.InputOption{Label: label, Description: description})
		}
		for _, option := range property.OneOf {
			label := option.Title
			if label == "" {
				label = fmt.Sprint(option.Const)
			}
			add(label, option.Description, option.Const)
		}
		for _, value := range property.Enum {
			add(fmt.Sprint(value), "", value)
		}
		if property.Type == "boolean" {
			add("true", "", true)
			add("false", "", false)
		}
		if len(field.values) == 0 {
			field.question.IsOther = true
		}
		fields = append(fields, field)
	}
	return fields, nil
}
