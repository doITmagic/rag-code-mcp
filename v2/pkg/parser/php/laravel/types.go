package laravel

// LaravelInfo contains Laravel-specific framework information extracted from a project
type LaravelInfo struct {
	Models      []EloquentModel `json:"models"`
	Controllers []Controller    `json:"controllers"`
	Routes      []Route         `json:"routes"`
	Migrations  []Migration     `json:"migrations,omitempty"`
	Middleware  []Middleware    `json:"middleware,omitempty"`
}

// EloquentModel represents a Laravel Eloquent model with ORM features
type EloquentModel struct {
	ClassName   string              `json:"class_name"`
	Namespace   string              `json:"namespace"`
	FullName    string              `json:"full_name"`
	Description string              `json:"description"`
	Table       string              `json:"table,omitempty"`       // $table property
	Fillable    []string            `json:"fillable,omitempty"`    // $fillable array
	Guarded     []string            `json:"guarded,omitempty"`     // $guarded array
	Casts       map[string]string   `json:"casts,omitempty"`       // $casts array
	Dates       []string            `json:"dates,omitempty"`       // $dates array (legacy)
	Hidden      []string            `json:"hidden,omitempty"`      // $hidden array
	Visible     []string            `json:"visible,omitempty"`     // $visible array
	Appends     []string            `json:"appends,omitempty"`     // $appends array
	Relations   []EloquentRelation  `json:"relations,omitempty"`   // Detected relations
	Scopes      []EloquentScope     `json:"scopes,omitempty"`      // Query scopes
	Attributes  []EloquentAttribute `json:"attributes,omitempty"`  // Accessors/Mutators
	PrimaryKey  string              `json:"primary_key,omitempty"` // $primaryKey
	Timestamps  bool                `json:"timestamps"`            // $timestamps (default true)
	SoftDeletes bool                `json:"soft_deletes"`          // Uses SoftDeletes trait
	FilePath    string              `json:"file_path"`
	StartLine   int                 `json:"start_line"`
	EndLine     int                 `json:"end_line"`
}

// EloquentRelation represents a relationship method in an Eloquent model
type EloquentRelation struct {
	Name         string `json:"name"`          // Method name (e.g., "posts")
	Type         string `json:"type"`          // hasOne, hasMany, belongsTo, belongsToMany, etc.
	RelatedModel string `json:"related_model"` // Related model class (e.g., "App\\Models\\Post")
	ForeignKey   string `json:"foreign_key,omitempty"`
	LocalKey     string `json:"local_key,omitempty"`
	Description  string `json:"description,omitempty"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
}

// EloquentScope represents a query scope (scopeMethodName)
type EloquentScope struct {
	Name        string `json:"name"`        // Scope name without "scope" prefix
	MethodName  string `json:"method_name"` // Full method name (e.g., "scopeActive")
	Description string `json:"description,omitempty"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
}

// EloquentAttribute represents an accessor or mutator
type EloquentAttribute struct {
	Name        string `json:"name"`        // Attribute name
	MethodName  string `json:"method_name"` // Method name (e.g., "getFullNameAttribute")
	Type        string `json:"type"`        // "accessor" or "mutator"
	Description string `json:"description,omitempty"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
}

// Controller represents a Laravel controller
type Controller struct {
	ClassName      string             `json:"class_name"`
	Namespace      string             `json:"namespace"`
	FullName       string             `json:"full_name"`
	Description    string             `json:"description"`
	BaseController string             `json:"base_controller,omitempty"` // Extends Controller/ApiController
	IsResource     bool               `json:"is_resource"`               // Resource controller
	IsApi          bool               `json:"is_api"`                    // API controller
	Actions        []ControllerAction `json:"actions"`
	Middleware     []string           `json:"middleware,omitempty"` // Middleware applied
	FilePath       string             `json:"file_path"`
	StartLine      int                `json:"start_line"`
	EndLine        int                `json:"end_line"`
}

// ControllerAction represents a controller method/action
type ControllerAction struct {
	Name        string   `json:"name"`        // Method name
	Description string   `json:"description"` // PHPDoc description
	HttpMethods []string `json:"http_methods,omitempty"`
	Route       string   `json:"route,omitempty"`      // Associated route pattern
	Parameters  []string `json:"parameters,omitempty"` // Method parameters
	Returns     string   `json:"returns,omitempty"`    // Return type
	StartLine   int      `json:"start_line"`
	EndLine     int      `json:"end_line"`
}

// Route represents a Laravel route definition
type Route struct {
	Method      string   `json:"method"` // GET, POST, PUT, DELETE, etc.
	URI         string   `json:"uri"`    // Route pattern (e.g., "/users/{id}")
	Name        string   `json:"name,omitempty"`
	Controller  string   `json:"controller,omitempty"` // Controller@action or FQCN
	Action      string   `json:"action,omitempty"`     // Action method name
	Middleware  []string `json:"middleware,omitempty"`
	Description string   `json:"description,omitempty"`
	FilePath    string   `json:"file_path"` // routes/web.php or routes/api.php
	Line        int      `json:"line"`
}

// Migration represents a database migration file
type Migration struct {
	ClassName   string   `json:"class_name"`
	Description string   `json:"description"`
	Table       string   `json:"table,omitempty"`      // Table being modified
	Operations  []string `json:"operations,omitempty"` // create, update, drop, etc.
	FilePath    string   `json:"file_path"`
	StartLine   int      `json:"start_line"`
	EndLine     int      `json:"end_line"`
}

// Middleware represents a middleware class
type Middleware struct {
	ClassName   string `json:"class_name"`
	Namespace   string `json:"namespace"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Alias       string `json:"alias,omitempty"` // Middleware alias in kernel
	FilePath    string `json:"file_path"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
}
