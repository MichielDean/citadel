package evaluate

// Dimension is a rubric dimension that the evaluator scores against.
// Each dimension measures a specific quality attribute with a 0-5 scale.
type Dimension string

const (
	// ContractCorrectness measures whether every method/function does what its
	// signature promises. Methods returning hardcoded values where computation is
	// expected are contract violations.
	ContractCorrectness Dimension = "contract_correctness"

	// IntegrationCoverage measures whether new code paths (DAO methods, search
	// filters, query builders, mapping functions) have integration tests that
	// exercise real infrastructure, not just mocks.
	IntegrationCoverage Dimension = "integration_coverage"

	// Coupling measures whether new code couples to specific entities when it
	// could be generic. Hardcoded table references, entity-specific imports in
	// generic utilities, and similar patterns lose points.
	Coupling Dimension = "coupling"

	// MigrationSafety measures whether database migrations follow safe practices:
	// quoted identifiers, DDL/DML separation, rollback safety, and meaningful
	// descriptions for reference data.
	MigrationSafety Dimension = "migration_safety"

	// IdiomFit measures whether the code uses the framework's idiomatic patterns
	// instead of fighting the framework. Uses Exists/NotExists instead of raw
	// SQL in Exposed, uses Set where UNIQUE constraints exist, etc.
	IdiomFit Dimension = "idiom_fit"

	// DRY measures whether repeated patterns are extracted into helpers instead
	// of copy-pasted. The same expression appearing 3+ times without extraction
	// loses points.
	DRY Dimension = "dry"

	// NamingClarity measures whether types and methods are honestly named.
	// Types that create wrong mental models (e.g., PermissionColumnName that
	// is not a column) lose points.
	NamingClarity Dimension = "naming_clarity"

	// ErrorMessages measures whether error messages are actionable. Generic
	// NoSuchElementException vs "Permission 'cps_enabled' not found in catalog"
	// — the latter tells the developer exactly what to fix.
	ErrorMessages Dimension = "error_messages"
)

// AllDimensions returns all rubric dimensions in canonical order.
func AllDimensions() []Dimension {
	return []Dimension{
		ContractCorrectness,
		IntegrationCoverage,
		Coupling,
		MigrationSafety,
		IdiomFit,
		DRY,
		NamingClarity,
		ErrorMessages,
	}
}

// DimensionDescription returns a human-readable description of what a dimension measures.
func DimensionDescription(d Dimension) string {
	switch d {
	case ContractCorrectness:
		return "Does every method do what its signature promises? Methods returning hardcoded values where computation is expected are contract violations."
	case IntegrationCoverage:
		return "Do new code paths (DAO methods, search filters, query builders) have integration tests against real infrastructure?"
	case Coupling:
		return "Is new code coupled to specific entities when it could be generic? Hardcoded entity references in generic utilities lose points."
	case MigrationSafety:
		return "Do migrations follow safe practices: quoted identifiers, DDL/DML separation, rollback safety, meaningful descriptions?"
	case IdiomFit:
		return "Does the code use the framework's idiomatic patterns? Exposed Exists/NotExists, Set for unique values, etc."
	case DRY:
		return "Are repeated patterns extracted into helpers? The same expression 3+ times without extraction loses points."
	case NamingClarity:
		return "Are types and methods honestly named? Types that create wrong mental models (e.g., PermissionColumnName that is not a column) lose points."
	case ErrorMessages:
		return "Are error messages actionable? Generic errors lose points; specific messages that tell the developer what to fix gain points."
	default:
		return "unknown dimension"
	}
}

// Score is a single dimension score on a 0-5 scale.
type Score struct {
	Dimension Dimension `json:"dimension"`
	Score     int       `json:"score"`      // 0-5
	Evidence  string    `json:"evidence"`   // specific code reference
	Suggested string    `json:"suggested"`  // what would make it a 5
}

// Result is a complete evaluation result for a single diff or PR.
type Result struct {
	Source      string  `json:"source"`      // e.g., "cistern" or "vibe-coded"
	Ticket      string  `json:"ticket"`     // e.g., "PROJ-123" or ""
	Branch      string  `json:"branch"`     // e.g., "feat/fix-thing"
	Commit      string  `json:"commit"`     // git SHA or "HEAD"
	Model      string   `json:"model"`      // LLM model used for evaluation
	Scores     []Score  `json:"scores"`
	TotalScore int      `json:"total_score"` // sum of all dimension scores
	MaxScore   int      `json:"max_score"`   // maximum possible score (5 * number of dimensions)
	Notes      string   `json:"notes"`       // free-form evaluation notes
	Timestamp  string   `json:"timestamp"`
}

// Percentage returns the score as a percentage of the maximum.
func (r *Result) Percentage() float64 {
	if r.MaxScore == 0 {
		return 0
	}
	return float64(r.TotalScore) / float64(r.MaxScore) * 100
}