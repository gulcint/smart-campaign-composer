package apispec

import "fmt"

// ValidateComponent validates data (one JSON document) against components.schemas.<name> of the
// spec and returns the problems (nil when valid). The E2E suite uses it for the SSE event payloads
// that the Transport does not read (openapi x-sse-events.schemas: ContentDeltaEvent,
// ContentInvalidEvent, ContentValidatedEvent, CampaignStatus; sse-events.md §0 "Makine okunur
// şemalar", DECISIONS.md D42).
func (s *Spec) ValidateComponent(name string, data []byte) []string {
	if s.model.Components == nil || s.model.Components.Schemas == nil {
		return []string{"spec has no components.schemas"}
	}
	proxy, ok := s.model.Components.Schemas.Get(name)
	if !ok || proxy == nil {
		return []string{fmt.Sprintf("spec has no components.schemas.%s", name)}
	}
	schema := proxy.Schema()
	if schema == nil {
		return []string{fmt.Sprintf("components.schemas.%s does not resolve", name)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, errs := s.validateSchema(schema, data); !ok || len(errs) > 0 {
		return describe("components.schemas."+name, errs)
	}
	return nil
}
