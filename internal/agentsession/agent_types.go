package agentsession

import (
	"encoding/json"
	"strings"

	"github.com/niczy/gitslice/internal/models"
)

type runnerAgentTypeCapabilities struct {
	AgentType                string   `json:"agent_type"`
	AgentTypeCamel           string   `json:"agentType"`
	DefaultAgentType         string   `json:"default_agent_type"`
	DefaultAgentTypeCamel    string   `json:"defaultAgentType"`
	SupportedAgentTypes      []string `json:"supported_agent_types"`
	SupportedAgentTypesCamel []string `json:"supportedAgentTypes"`
}

func NormalizeAgentType(agentType string) string {
	return strings.ToLower(strings.TrimSpace(agentType))
}

func IsSupportedAgentType(agentType string) bool {
	_, ok := supportedAgentTypes[NormalizeAgentType(agentType)]
	return ok
}

func RunnerSupportedAgentTypes(runner *models.AgentRunner) []string {
	if runner == nil {
		return []string{defaultAgentType}
	}
	var values []string
	if len(runner.Capabilities) > 0 {
		var caps runnerAgentTypeCapabilities
		if err := json.Unmarshal(runner.Capabilities, &caps); err == nil {
			supported := normalizeAgentTypeList(append(caps.SupportedAgentTypes, caps.SupportedAgentTypesCamel...))
			if len(supported) > 0 {
				return supported
			}
			values = append(values, caps.AgentType, caps.AgentTypeCamel)
		}
	}
	values = append(values, runner.AgentType)
	normalized := normalizeAgentTypeList(values)
	if len(normalized) == 0 {
		return []string{defaultAgentType}
	}
	return normalized
}

func RunnerDefaultAgentType(runner *models.AgentRunner) string {
	if runner == nil {
		return defaultAgentType
	}
	supported := RunnerSupportedAgentTypes(runner)
	if len(runner.Capabilities) > 0 {
		var caps runnerAgentTypeCapabilities
		if err := json.Unmarshal(runner.Capabilities, &caps); err == nil {
			for _, candidate := range []string{
				caps.DefaultAgentType,
				caps.DefaultAgentTypeCamel,
				caps.AgentType,
				caps.AgentTypeCamel,
			} {
				if runnerAgentTypesContain(supported, candidate) {
					return NormalizeAgentType(candidate)
				}
			}
		}
	}
	if runnerAgentTypesContain(supported, runner.AgentType) {
		return NormalizeAgentType(runner.AgentType)
	}
	if len(supported) > 0 {
		return supported[0]
	}
	return defaultAgentType
}

func RunnerSupportsAgentType(runner *models.AgentRunner, agentType string) bool {
	return runnerAgentTypesContain(RunnerSupportedAgentTypes(runner), agentType)
}

func normalizeAgentTypeList(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		agentType := NormalizeAgentType(value)
		if agentType == "" || !IsSupportedAgentType(agentType) {
			continue
		}
		seen[agentType] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for _, agentType := range SupportedAgentTypes() {
		if _, ok := seen[agentType]; ok {
			out = append(out, agentType)
		}
	}
	return out
}

func runnerAgentTypesContain(values []string, agentType string) bool {
	agentType = NormalizeAgentType(agentType)
	if agentType == "" {
		return false
	}
	for _, value := range values {
		if value == agentType {
			return true
		}
	}
	return false
}
