package playspec

import "github.com/fibegg/fibe-distilled/internal/domain"

// ResourceInput is the normalized input for creating a Playspec resource.
type ResourceInput struct {
	// Name is the client-visible Playspec name.
	Name string
	// Description is optional SDK metadata.
	Description *string
	// BaseComposeYAML is the Compose document stored for the Playspec.
	BaseComposeYAML string
	// PersistVolumes is rejected when true because fibe-distilled is stateless.
	PersistVolumes *bool
	// Services is optional SDK service metadata folded into the Compose document.
	Services []any
}

// NewResource validates and converts input into a Playspec row.
func NewResource(input ResourceInput) (domain.Playspec, error) {
	return payload{
		Name:            input.Name,
		Description:     input.Description,
		BaseComposeYAML: input.BaseComposeYAML,
		PersistVolumes:  input.PersistVolumes,
		Services:        input.Services,
	}.toDomain()
}
