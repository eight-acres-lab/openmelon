package generation

// Request describes a generation request for a model or tool adapter.
type Request struct {
	ArtifactType string
	Prompt       string
	Model        string
	Params       map[string]string
	// Intent is the operator's free-text intent, used as the LLM User message.
	Intent string
}
