# Internal Util Package (Service)

The `internalutil` package under `service` provides data conversion helpers specifically tailored for the service-layer transformations.

## Key Functions

- **Vector Conversions**: Converting between `[]float64` (typical LLM output) and `[]float32` (efficient storage format for Qdrant).
- **Structure Mapping**: Mapping between internal domain objects and generic interface maps required for database payloads.

## Rationale

These helpers are localized to the service directory as they often bridge the gap between the `pkg` interfaces and the specific needs of the `engine` and `tools` implementations.
