// Package chunk defines the shared semantic-chunk type produced by the agentic
// chunker and consumed by the vector store.
package chunk

// Chunk is one semantic chunk of a document.
type Chunk struct {
	// TextToEmbed is the embedding input (global context + the generated content).
	TextToEmbed string
	// Content is the model-generated, retrieval-optimized chunk text. It is what
	// gets stored and returned in search results — not the original document
	// markup.
	Content string
	// Header is a short title for the chunk.
	Header string
}
