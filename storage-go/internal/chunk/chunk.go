// Package chunk defines the shared semantic-chunk type produced by the agentic
// chunker and consumed by the vector store.
package chunk

// Chunk is one semantic chunk of a document.
type Chunk struct {
	// TextToEmbed is the embedding input (context + title + summary + content).
	TextToEmbed string
	// OriginalHTML is the rendered HTML stored alongside the vector.
	OriginalHTML string
	// Header is a short title for the chunk.
	Header string
}
