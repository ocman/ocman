package factorymod

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
)

func factoryRecoveryChoices(req mcplib.CallToolRequest) ([]string, error) {
	raw, err := req.RequireString("choices_json")
	if err != nil || raw == "" {
		return []string{}, nil
	}
	var choices []string
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&choices) != nil || decoder.Decode(&struct{}{}) != io.EOF || choices == nil {
		return nil, errors.New("invalid")
	}
	return choices, nil
}

func factoryProposalManifest(req mcplib.CallToolRequest) (factory.ProposalManifest, *mcplib.CallToolResult) {
	raw, err := req.RequireString("manifest_json")
	if err != nil {
		return factory.ProposalManifest{}, mcplib.NewToolResultError("manifest_json is required")
	}
	var manifest factory.ProposalManifest
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return factory.ProposalManifest{}, mcplib.NewToolResultError("manifest_json is invalid")
	}
	return manifest, nil
}

func optionalFormulaID(req mcplib.CallToolRequest) string {
	id, _ := req.RequireString("formula_id")
	return id
}
func factoryEpicID(req mcplib.CallToolRequest) (string, *mcplib.CallToolResult) {
	id, err := req.RequireString("epic_id")
	if err != nil || id == "" {
		return "", mcplib.NewToolResultError("epic_id is required")
	}
	return id, nil
}

func factoryProjectOverrides(req mcplib.CallToolRequest) (map[string]int, error) {
	arguments, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return nil, errors.New("invalid")
	}
	raw, ok := arguments["project_overrides"]
	if !ok {
		return nil, errors.New("missing")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var overrides map[string]int
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&overrides); err != nil || decoder.Decode(&struct{}{}) != io.EOF || overrides == nil {
		return nil, errors.New("invalid")
	}
	return overrides, nil
}
func factoryToolError(err error) *mcplib.CallToolResult {
	logrus.WithError(err).Error("Factory MCP request failed")
	if errors.Is(err, factory.ErrActionNotPermitted) {
		return mcplib.NewToolResultError("factory action is not permitted")
	}
	if errors.Is(err, factory.ErrWorkEpicNotFound) {
		return mcplib.NewToolResultError("factory epic not found")
	}
	if errors.Is(err, factory.ErrInstantiationConflict) {
		return mcplib.NewToolResultError("factory instantiation conflict")
	}
	if errors.Is(err, factory.ErrEpicIDTaken) {
		return mcplib.NewToolResultError(err.Error())
	}
	if errors.Is(err, factory.ErrFormulaNotFound) {
		return mcplib.NewToolResultError("factory Formula not found")
	}
	if errors.Is(err, factory.ErrInvalidFormula) {
		return mcplib.NewToolResultError(err.Error())
	}
	if errors.Is(err, factory.ErrInvalidRequest) {
		return mcplib.NewToolResultError(err.Error())
	}
	if errors.Is(err, factory.ErrProjectNotLocalGit) {
		return mcplib.NewToolResultError(factory.ErrProjectNotLocalGit.Error())
	}
	if errors.Is(err, factory.ErrAcknowledgementRequired) {
		return mcplib.NewToolResultError(factory.ErrAcknowledgementRequired.Error())
	}
	if errors.Is(err, factory.ErrFactoryUnavailable) {
		return mcplib.NewToolResultError(factory.ErrFactoryUnavailable.Error())
	}
	return mcplib.NewToolResultError(err.Error())
}
