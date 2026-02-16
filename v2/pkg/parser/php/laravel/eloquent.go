package laravel

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
)

// EloquentRelation represents a relationship between models
type EloquentRelation struct {
	Name       string `json:"name"`
	Type       string `json:"type"`       // belongsTo, hasMany, etc.
	Related    string `json:"related"`    // Related class name
	ForeignKey string `json:"foreign_key,omitempty"`
	LocalKey   string `json:"local_key,omitempty"`
}

// Detector detects Laravel-specific patterns in PHP code
type Detector struct {
	astHelper *ASTHelper
}

// NewDetector creates a new Laravel pattern detector
func NewDetector() *Detector {
	return &Detector{
		astHelper: NewASTHelper(),
	}
}

// IsEloquentModel checks if a class extends an Eloquent base model
func (d *Detector) IsEloquentModel(n *ast.StmtClass, extends string) bool {
	if extends == "" {
		return false
	}
	// Check if extends Model or Illuminate\Database\Eloquent\Model
	if extends == "Model" ||
		extends == "Eloquent\\Model" ||
		extends == "Illuminate\\Database\\Eloquent\\Model" ||
		strings.HasSuffix(extends, "\\Model") ||
		extends == "Authenticatable" ||
		strings.HasSuffix(extends, "\\Authenticatable") {
		return true
	}
	return false
}

// ExtractEloquentMetadata extracts Eloquent-specific features from a class node
func (d *Detector) ExtractEloquentMetadata(n *ast.StmtClass) map[string]any {
	meta := make(map[string]any)
	meta["laravel_type"] = "model"

	h := d.astHelper
	if table := h.ExtractStringProperty(n, "table"); table != "" {
		meta["table"] = table
	}
	if fillable := h.ExtractStringArray(n, "fillable"); len(fillable) > 0 {
		meta["fillable"] = fillable
	}
	if guarded := h.ExtractStringArray(n, "guarded"); len(guarded) > 0 {
		meta["guarded"] = guarded
	}
	if hidden := h.ExtractStringArray(n, "hidden"); len(hidden) > 0 {
		meta["hidden"] = hidden
	}

	// Relations are extracted from methods
	relations := d.ExtractRelations(n)
	if len(relations) > 0 {
		meta["relations"] = relations
	}

	return meta
}

// ExtractRelations scans class methods for Eloquent relationship calls
func (d *Detector) ExtractRelations(n *ast.StmtClass) []EloquentRelation {
	var relations []EloquentRelation

	for _, stmt := range n.Stmts {
		if method, ok := stmt.(*ast.StmtClassMethod); ok {
			rel := d.detectRelationInMethod(method)
			if rel != nil {
				relations = append(relations, *rel)
			}
		}
	}

	return relations
}

func (d *Detector) detectRelationInMethod(method *ast.StmtClassMethod) *EloquentRelation {
	// Look for return $this->belongsTo(...), etc.
	// This is a simplified version of the v1 logic
	if method.Stmt == nil {
		return nil
	}

	compound, ok := method.Stmt.(*ast.StmtCompound)
	if !ok {
		return nil
	}

	for _, stmt := range compound.Stmts {
		if ret, ok := stmt.(*ast.StmtReturn); ok {
			if call, ok := ret.Expr.(*ast.ExprMethodCall); ok {
				methodName := d.extractIdentifier(call.Method)
				types := map[string]bool{
					"belongsTo": true, "hasMany": true, "hasOne": true,
					"belongsToMany": true, "morphTo": true, "morphMany": true,
					"morphOne": true, "morphToMany": true, "morphedByMany": true,
				}

				if types[methodName] {
					rel := &EloquentRelation{
						Name: d.extractIdentifier(method.Name),
						Type: methodName,
					}
					// Extract related class from first argument
					if len(call.Args) > 0 {
						if arg, ok := call.Args[0].(*ast.Argument); ok {
							rel.Related = d.extractRelatedClass(arg.Expr)
						}
					}
					return rel
				}
			}
		}
	}

	return nil
}

// RouteInfo represents metadata for a Laravel route
type RouteInfo struct {
	Method     string `json:"method"`      // GET, POST, etc.
	Uri        string `json:"uri"`         // /users/{id}
	Action     string `json:"action"`      // UserController@index
	Controller string `json:"controller"`  // UserController
	Function   string `json:"function"`    // index
	Middleware []string `json:"middleware,omitempty"`
}

// IsRouteDefinition checks if a static call is a Laravel route definition
func (d *Detector) IsRouteDefinition(n *ast.ExprStaticCall) bool {
	class := d.astHelper.ExtractNodeName(n.Class)
	if class != "Route" && class != "\\Route" && !strings.HasSuffix(class, "\\Route") {
		return false
	}

	method := d.astHelper.ExtractIdentifier(n.Method)
	methods := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true,
		"delete": true, "options": true, "any": true, "match": true,
		"resource": true, "apiResource": true,
	}

	return methods[strings.ToLower(method)]
}

// ExtractRouteInfo extracts details from a Route static call
func (d *Detector) ExtractRouteInfo(n *ast.ExprStaticCall) *RouteInfo {
	if !d.IsRouteDefinition(n) {
		return nil
	}

	info := &RouteInfo{
		Method: d.astHelper.ExtractIdentifier(n.Method),
	}

	// First argument is usually the URI
	if len(n.Args) > 0 {
		if arg, ok := n.Args[0].(*ast.Argument); ok {
			info.Uri = d.astHelper.ExtractStringFromExpr(arg.Expr)
		}
	}

	// Second argument is the action (string or array)
	if len(n.Args) > 1 {
		if arg, ok := n.Args[1].(*ast.Argument); ok {
			d.populateActionInfo(info, arg.Expr)
		}
	}

	return info
}

func (d *Detector) populateActionInfo(info *RouteInfo, expr ast.Vertex) {
	switch e := expr.(type) {
	case *ast.ScalarString:
		// 'UserController@index'
		action := strings.Trim(string(e.Value), "'\"")
		info.Action = action
		if parts := strings.Split(action, "@"); len(parts) == 2 {
			info.Controller = parts[0]
			info.Function = parts[1]
		}
	case *ast.ExprArray:
		// [UserController::class, 'index']
		if len(e.Items) >= 2 {
			// Controller
			if item, ok := e.Items[0].(*ast.ExprArrayItem); ok {
				info.Controller = d.astHelper.ExtractNodeName(item.Val)
			}
			// Action
			if item, ok := e.Items[1].(*ast.ExprArrayItem); ok {
				info.Function = d.astHelper.ExtractStringFromExpr(item.Val)
			}
			if info.Controller != "" && info.Function != "" {
				info.Action = info.Controller + "@" + info.Function
			}
		}
	}
}

func (d *Detector) extractIdentifier(n ast.Vertex) string {
	if ident, ok := n.(*ast.Identifier); ok {
		return string(ident.Value)
	}
	return ""
}

func (d *Detector) extractRelatedClass(n ast.Vertex) string {
	switch expr := n.(type) {
	case *ast.ExprClassConstFetch:
		// User::class
		return d.extractName(expr.Class)
	case *ast.ScalarString:
		// 'App\Models\User'
		return strings.Trim(string(expr.Value), "'\"")
	}
	return ""
}

func (d *Detector) extractName(n ast.Vertex) string {
	switch nm := n.(type) {
	case *ast.Name:
		var parts []string
		for _, p := range nm.Parts {
			if np, ok := p.(*ast.NamePart); ok {
				parts = append(parts, string(np.Value))
			}
		}
		return strings.Join(parts, "\\")
	}
	return ""
}
