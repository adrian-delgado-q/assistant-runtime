package llm

import (
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type systemPromptYAML struct {
	Identity      string   `yaml:"identity"`
	BusinessRules []string `yaml:"business_rules"`
	QuoteFields   []string `yaml:"quote_fields_needed"`
	Workflow      string   `yaml:"workflow"`
}

var compiledSystemPrompt string

// LoadPrompt reads and compiles the YAML prompt template at startup.
// Call once from main(); panics on failure so bad config surfaces immediately.
func LoadPrompt(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("llm: failed to read system prompt: %v", err)
	}

	var p systemPromptYAML
	if err := yaml.Unmarshal(data, &p); err != nil {
		log.Fatalf("llm: failed to parse system prompt YAML: %v", err)
	}

	rules := make([]string, len(p.BusinessRules))
	for i, r := range p.BusinessRules {
		rules[i] = fmt.Sprintf("- %s", r)
	}

	compiledSystemPrompt = strings.TrimSpace(fmt.Sprintf(`
%s

Business Rules:
%s

Quote Fields Needed: %s

Workflow: %s

You MUST respond ONLY with a valid JSON object matching this exact schema — no extra text:
{
  "reply_to_user": "<string: message to send to the customer>",
  "extracted_data": {
    "address": "<string or 'unknown'>",
    "elevator_access": "<string or 'unknown'>",
    "stairs": "<string or 'unknown'>",
    "inventory": "<string or 'unknown'>"
  },
  "action": "<one of: continue | handoff | schedule>"
}
`,
		p.Identity,
		strings.Join(rules, "\n"),
		strings.Join(p.QuoteFields, ", "),
		p.Workflow,
	))

	log.Println("llm: system prompt loaded")
}

// SystemPrompt returns the compiled prompt string.
func SystemPrompt() string {
	return compiledSystemPrompt
}

// SetSystemPromptForTest overrides the compiled prompt. Only call this from tests.
func SetSystemPromptForTest(prompt string) {
	compiledSystemPrompt = prompt
}
