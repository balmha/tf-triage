// Package parser handles ingestion, validation, and token-optimization of
// Terraform/OpenTofu JSON plan output.
package parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// Typed errors for actionable CLI messaging
// ---------------------------------------------------------------------------

// ErrMalformedJSON indicates the input is not valid JSON.
var ErrMalformedJSON = errors.New("input is not valid JSON")

// ErrNotTerraformPlan indicates the JSON does not match the expected
// Terraform/OpenTofu plan schema.
var ErrNotTerraformPlan = errors.New("input does not appear to be a Terraform/OpenTofu plan")

// ErrStreamingFormat indicates the input is terraform's streaming JSON log
// format rather than the plan schema.
var ErrStreamingFormat = errors.New("input appears to be Terraform's streaming log format (line-delimited JSON), not a plan schema.\n  Use: terraform show -json <planfile> | tf-triage\n  Or:  terraform plan -out=plan.tfplan && terraform show -json plan.tfplan | tf-triage")

// ErrNoChanges indicates the plan contains no resource changes to analyze.
var ErrNoChanges = errors.New("plan contains no resource changes")

// ---------------------------------------------------------------------------
// Plan schema types
// ---------------------------------------------------------------------------

// Plan represents the top-level structure of `terraform show -json` or
// `terraform plan -json` output.
type Plan struct {
	FormatVersion    string           `json:"format_version"`
	TerraformVersion string           `json:"terraform_version"`
	ResourceChanges  []ResourceChange `json:"resource_changes"`
	// We intentionally ignore output_changes, prior_state, configuration, etc.
	// to keep scope focused on resource-level analysis.
}

// ResourceChange represents a single resource change entry.
type ResourceChange struct {
	Address      string `json:"address"`
	Mode         string `json:"mode"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	ProviderName string `json:"provider_name"`
	Change       Change `json:"change"`
}

// Change holds the action list and before/after state for a resource.
type Change struct {
	Actions         []string               `json:"actions"`
	Before          map[string]interface{} `json:"before"`
	After           map[string]interface{} `json:"after"`
	AfterUnknown    map[string]interface{} `json:"after_unknown"`
	BeforeSensitive interface{}            `json:"before_sensitive"`
	AfterSensitive  interface{}            `json:"after_sensitive"`
}

// ---------------------------------------------------------------------------
// Optimized output types (token-reduced for LLM consumption)
// ---------------------------------------------------------------------------

// OptimizedPlan is the compact representation sent to the LLM.
type OptimizedPlan struct {
	TerraformVersion string            `json:"terraform_version,omitempty"`
	Summary          ChangeSummary     `json:"summary"`
	Changes          []OptimizedChange `json:"changes"`
}

// ChangeSummary provides aggregate counts for quick orientation.
type ChangeSummary struct {
	Create int `json:"create"`
	Update int `json:"update"`
	Delete int `json:"delete"`
	NoOp   int `json:"no_op,omitempty"`
}

// OptimizedChange is a compact representation of a single resource change.
type OptimizedChange struct {
	Address  string                 `json:"address"`
	Type     string                 `json:"type"`
	Provider string                 `json:"provider"`
	Actions  []string               `json:"actions"`
	Before   map[string]interface{} `json:"before,omitempty"`
	After    map[string]interface{} `json:"after,omitempty"`
	Diff     map[string]interface{} `json:"diff,omitempty"`
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Parse validates and decodes raw JSON bytes into a Plan.
// Supports two input formats:
//   1. Standard plan JSON (from `terraform show -json <planfile>`)
//   2. Streaming line-delimited JSON (from `terraform plan -json`)
//
// Returns typed errors suitable for user-facing messaging.
func Parse(data []byte) (*Plan, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedJSON)
	}

	// Detect Terraform's streaming log format (line-delimited JSON with @level/@message fields).
	// If detected, parse the streaming lines to extract planned changes.
	if isStreamingFormat(data) {
		return parseStreamingFormat(data)
	}

	// First pass: verify it's valid JSON at all
	if !json.Valid(data) {
		return nil, fmt.Errorf("%w: could not decode input as JSON", ErrMalformedJSON)
	}

	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}

	// Schema validation: a Terraform plan must have format_version
	if plan.FormatVersion == "" {
		return nil, fmt.Errorf(
			"%w: missing 'format_version' field (expected output from 'terraform show -json' or 'terraform plan -json')",
			ErrNotTerraformPlan,
		)
	}

	// Ensure resource_changes exists (even if empty — we'll check count separately)
	if plan.ResourceChanges == nil {
		return nil, fmt.Errorf(
			"%w: missing 'resource_changes' array",
			ErrNotTerraformPlan,
		)
	}

	if len(plan.ResourceChanges) == 0 {
		return nil, ErrNoChanges
	}

	return &plan, nil
}

// Optimize strips redundant metadata, computes diffs for updates, and
// produces a token-efficient payload for LLM analysis.
func Optimize(plan *Plan) *OptimizedPlan {
	opt := &OptimizedPlan{
		TerraformVersion: plan.TerraformVersion,
		Changes:          make([]OptimizedChange, 0, len(plan.ResourceChanges)),
	}

	for _, rc := range plan.ResourceChanges {
		// Tally summary
		for _, action := range rc.Change.Actions {
			switch action {
			case "create":
				opt.Summary.Create++
			case "update":
				opt.Summary.Update++
			case "delete":
				opt.Summary.Delete++
			case "no-op", "read":
				opt.Summary.NoOp++
			}
		}

		oc := OptimizedChange{
			Address:  rc.Address,
			Type:     rc.Type,
			Provider: shortenProvider(rc.ProviderName),
			Actions:  rc.Change.Actions,
		}

		switch {
		case hasAction(rc.Change.Actions, "create"):
			oc.After = stripMetadata(rc.Change.After)

		case hasAction(rc.Change.Actions, "delete"):
			oc.Before = stripMetadata(rc.Change.Before)

		case hasAction(rc.Change.Actions, "update"):
			oc.Diff = computeDiff(rc.Change.Before, rc.Change.After)

		case hasAction(rc.Change.Actions, "no-op"), hasAction(rc.Change.Actions, "read"):
			// Skip no-op resources entirely to save tokens
			continue

		default:
			oc.Before = stripMetadata(rc.Change.Before)
			oc.After = stripMetadata(rc.Change.After)
		}

		opt.Changes = append(opt.Changes, oc)
	}

	return opt
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func hasAction(actions []string, target string) bool {
	for _, a := range actions {
		if a == target {
			return true
		}
	}
	return false
}

// shortenProvider trims the registry prefix for readability.
// "registry.terraform.io/hashicorp/aws" → "hashicorp/aws"
func shortenProvider(provider string) string {
	const prefix = "registry.terraform.io/"
	if len(provider) > len(prefix) && provider[:len(prefix)] == prefix {
		return provider[len(prefix):]
	}
	return provider
}

// stripMetadata removes Terraform-internal fields that waste tokens without
// adding analytical value for the LLM.
func stripMetadata(attrs map[string]interface{}) map[string]interface{} {
	if attrs == nil {
		return nil
	}

	skipKeys := map[string]bool{
		"id":       true,
		"timeouts": true,
	}

	cleaned := make(map[string]interface{}, len(attrs))
	for k, v := range attrs {
		if skipKeys[k] {
			continue
		}
		// Strip deeply nested null values to reduce noise
		if v == nil {
			continue
		}
		cleaned[k] = v
	}

	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// computeDiff returns only attributes that changed between before and after,
// formatted as from/to pairs for clear LLM comprehension.
func computeDiff(before, after map[string]interface{}) map[string]interface{} {
	if before == nil && after == nil {
		return nil
	}

	diff := make(map[string]interface{})

	// Attributes added or modified
	for key, afterVal := range after {
		beforeVal, existed := before[key]
		if !existed {
			diff[key] = map[string]interface{}{"added": afterVal}
			continue
		}

		bJSON, _ := json.Marshal(beforeVal)
		aJSON, _ := json.Marshal(afterVal)
		if string(bJSON) != string(aJSON) {
			diff[key] = map[string]interface{}{"from": beforeVal, "to": afterVal}
		}
	}

	// Attributes removed
	for key, beforeVal := range before {
		if _, exists := after[key]; !exists {
			diff[key] = map[string]interface{}{"removed": beforeVal}
		}
	}

	if len(diff) == 0 {
		return nil
	}
	return diff
}

// isStreamingFormat detects Terraform's streaming JSON log output.
// When you run `terraform plan -json`, Terraform emits one JSON object per line
// with fields like "@level", "@message", "type".
func isStreamingFormat(data []byte) bool {
	// Check the first line only
	firstLine := data
	if idx := bytes.IndexByte(data, '\n'); idx > 0 {
		firstLine = data[:idx]
	}

	// Quick heuristic: streaming format always has "@level" or "@message" keys
	return bytes.Contains(firstLine, []byte(`"@level"`)) ||
		bytes.Contains(firstLine, []byte(`"@message"`)) ||
		bytes.Contains(firstLine, []byte(`"type":"version"`))
}

// ---------------------------------------------------------------------------
// Streaming format types and parser
// ---------------------------------------------------------------------------

// streamLine represents a single line from `terraform plan -json` output.
type streamLine struct {
	Type      string          `json:"type"`
	Terraform string          `json:"terraform"`
	Change    *streamChange   `json:"change"`
}

// streamChange represents the "change" object in a planned_change line.
type streamChange struct {
	Resource streamResource `json:"resource"`
	Action   string         `json:"action"`
}

// streamResource represents resource metadata in a planned_change line.
type streamResource struct {
	Addr            string `json:"addr"`
	ImpliedProvider string `json:"implied_provider"`
	ResourceType    string `json:"resource_type"`
	ResourceName    string `json:"resource_name"`
}

// parseStreamingFormat extracts plan data from Terraform's streaming JSON log output.
// It reads each line, picks out "planned_change" entries, and constructs a Plan.
func parseStreamingFormat(data []byte) (*Plan, error) {
	plan := &Plan{
		FormatVersion:   "streaming",
		ResourceChanges: make([]ResourceChange, 0),
	}

	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var sl streamLine
		if err := json.Unmarshal(line, &sl); err != nil {
			// Skip malformed lines (e.g., stderr mixed in)
			continue
		}

		// Extract terraform version from the version line
		if sl.Type == "version" && sl.Terraform != "" {
			plan.TerraformVersion = sl.Terraform
		}

		// Extract resource changes from planned_change lines
		if sl.Type == "planned_change" && sl.Change != nil {
			rc := ResourceChange{
				Address:      sl.Change.Resource.Addr,
				Type:         sl.Change.Resource.ResourceType,
				Name:         sl.Change.Resource.ResourceName,
				ProviderName: sl.Change.Resource.ImpliedProvider,
				Change: Change{
					Actions: []string{sl.Change.Action},
				},
			}
			plan.ResourceChanges = append(plan.ResourceChanges, rc)
		}
	}

	if len(plan.ResourceChanges) == 0 {
		return nil, fmt.Errorf("%w: no planned_change entries found in streaming output", ErrNoChanges)
	}

	return plan, nil
}
