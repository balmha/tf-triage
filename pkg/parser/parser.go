// Package parser handles ingestion, validation, and token-optimization of
// Terraform/OpenTofu JSON plan output.
package parser

import (
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
// Returns typed errors suitable for user-facing messaging.
func Parse(data []byte) (*Plan, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedJSON)
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
