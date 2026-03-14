package adapters

import (
	"context"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/errata-app/errata-cli/internal/capabilities"
	"github.com/errata-app/errata-cli/internal/models"
)

// BedrockAdapter implements ModelAdapter for Amazon Bedrock using the Converse API.
//
// Model IDs are configured with a "bedrock/" prefix (e.g. "bedrock/anthropic.claude-sonnet-4-20250514-v1:0").
// The prefix is stripped before the API call; the full prefixed ID is preserved for display and logging.
//
// Authentication uses the AWS SDK default credential chain:
//   - AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY environment variables
//   - AWS_PROFILE for named profiles in ~/.aws/credentials
//   - EC2/ECS/Lambda instance roles
//
// Set AWS_REGION (or AWS_DEFAULT_REGION) to specify the Bedrock endpoint region.
type BedrockAdapter struct {
	modelID     string // full ID as configured, e.g. "bedrock/anthropic.claude-sonnet-4-20250514-v1:0"
	bareModelID string // modelID with "bedrock/" stripped; sent to the Converse API
	region      string // AWS region for the Bedrock endpoint
}

// NewBedrockAdapter creates a BedrockAdapter.
func NewBedrockAdapter(modelID, region string) *BedrockAdapter {
	return &BedrockAdapter{
		modelID:     modelID,
		bareModelID: strings.TrimPrefix(modelID, "bedrock/"),
		region:      region,
	}
}

func (a *BedrockAdapter) ID() string { return a.modelID }

// Capabilities infers defaults from the sub-provider in the Bedrock model ID
// (e.g. "anthropic.claude-*" → anthropic defaults).
func (a *BedrockAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	// Bedrock model IDs use "provider.model" format — infer sub-provider.
	if i := strings.Index(a.bareModelID, "."); i >= 0 {
		subProvider := a.bareModelID[:i]
		caps := capabilities.DefaultCapabilities(subProvider, a.bareModelID)
		caps.ModelID = a.modelID
		caps.Provider = "bedrock"
		return caps
	}
	return capabilities.DefaultCapabilities("bedrock", a.bareModelID)
}

func (a *BedrockAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	start := time.Now()
	qualifiedID := bedrockQualifiedID(a.bareModelID)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(a.region))
	if err != nil {
		return BuildErrorResponse(a.modelID, qualifiedID, start, 0, 0, 0, err), err
	}
	client := bedrockruntime.NewFromConfig(awsCfg)

	return runBedrockAgentLoop(ctx, &bedrockRunConfig{
		client:      client,
		modelID:     a.modelID,
		bareModelID: a.bareModelID,
		qualifiedID: qualifiedID,
	}, history, prompt, onEvent)
}

func init() {
	var _ models.ModelAdapter = (*BedrockAdapter)(nil)
}

// bedrockQualifiedID returns a pricing-compatible qualified ID for a Bedrock model.
// It maps provider-prefixed Bedrock model IDs to the OpenRouter-style "provider/model" format.
// E.g. "anthropic.claude-sonnet-4-20250514-v1:0" → "anthropic/claude-sonnet-4-20250514-v1:0".
func bedrockQualifiedID(bareModelID string) string {
	if provider, model, ok := strings.Cut(bareModelID, "."); ok {
		// Strip Bedrock version suffix (":0", ":1") for pricing lookup.
		if j := strings.LastIndex(model, ":"); j >= 0 {
			model = model[:j]
		}
		// Strip Bedrock "-v1" suffix.
		if strings.HasSuffix(model, "-v1") || strings.HasSuffix(model, "-v2") {
			model = model[:len(model)-3]
		}
		return provider + "/" + model
	}
	return bareModelID
}
