package laravel

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php"
)

// EloquentAnalyzer extracts Eloquent model information from PHP package info
type EloquentAnalyzer struct {
	packageInfo *php.PackageInfo
	astHelper   *ASTPropertyExtractor
}

// NewEloquentAnalyzer creates a new Eloquent analyzer
func NewEloquentAnalyzer(packageInfo *php.PackageInfo) *EloquentAnalyzer {
	return &EloquentAnalyzer{
		packageInfo: packageInfo,
		astHelper:   NewASTPropertyExtractor(),
	}
}

// AnalyzeModels detects Eloquent models in the package and extracts Laravel-specific features
func (a *EloquentAnalyzer) AnalyzeModels() []EloquentModel {
	var models []EloquentModel

	for _, class := range a.packageInfo.Classes {
		if a.isEloquentModel(class) {
			model := a.extractEloquentModel(class)
			models = append(models, model)
		}
	}

	return models
}

// isEloquentModel checks if a class extends an Eloquent base model or a Laravel
// Authenticatable user model.
func (a *EloquentAnalyzer) isEloquentModel(class php.ClassInfo) bool {
	if class.Extends == "" {
		return false
	}

	// Check if extends Model or Illuminate\Database\Eloquent\Model
	extends := class.Extends
	if extends == "Model" ||
		extends == "Eloquent\\Model" ||
		extends == "Illuminate\\Database\\Eloquent\\Model" ||
		strings.HasSuffix(extends, "\\Model") {
		return true
	}

	// Treat Laravel user classes that extend Authenticatable as Eloquent models
	if extends == "Authenticatable" || strings.HasSuffix(extends, "\\Authenticatable") {
		return true
	}

	return false
}

// extractEloquentModel extracts Eloquent-specific features from a class
func (a *EloquentAnalyzer) extractEloquentModel(class php.ClassInfo) EloquentModel {
	model := EloquentModel{
		ClassName:   class.Name,
		Namespace:   class.Namespace,
		FullName:    class.FullName,
		Description: class.Description,
		FilePath:    class.FilePath,
		StartLine:   class.StartLine,
		EndLine:     class.EndLine,
		Timestamps:  true, // Default in Laravel
		SoftDeletes: a.usesSoftDeletes(class),
	}

	// Extract property arrays ($fillable, $guarded, $casts, etc.)
	model.Fillable = a.extractStringArray(class, "fillable")
	model.Guarded = a.extractStringArray(class, "guarded")
	model.Hidden = a.extractStringArray(class, "hidden")
	model.Visible = a.extractStringArray(class, "visible")
	model.Appends = a.extractStringArray(class, "appends")
	model.Dates = a.extractStringArray(class, "dates")
	model.Casts = a.extractCastsArray(class)
	model.Table = a.extractStringProperty(class, "table")
	model.PrimaryKey = a.extractStringProperty(class, "primaryKey")

	// Extract relations from methods
	model.Relations = a.extractRelations(class)

	// Extract scopes
	model.Scopes = a.extractScopes(class)

	// Extract accessors and mutators
	model.Attributes = a.extractAttributes(class)

	return model
}

// usesSoftDeletes checks if the model uses the SoftDeletes trait
func (a *EloquentAnalyzer) usesSoftDeletes(class php.ClassInfo) bool {
	for _, trait := range class.Uses {
		if strings.Contains(trait, "SoftDeletes") {
			return true
		}
	}
	return false
}

// extractStringArray extracts a protected/private array property (e.g., $fillable)
func (a *EloquentAnalyzer) extractStringArray(class php.ClassInfo, propertyName string) []string {
	// Get AST node for this class
	if a.packageInfo.ClassNodes == nil {
		return nil
	}

	classNode, exists := a.packageInfo.ClassNodes[class.FullName]
	if !exists {
		return nil
	}

	result := a.astHelper.ExtractStringArrayFromClass(classNode, propertyName)
	return result
}

// extractCastsArray extracts the $casts array as a map
func (a *EloquentAnalyzer) extractCastsArray(class php.ClassInfo) map[string]string {
	// Get AST node for this class
	if a.packageInfo.ClassNodes == nil {
		return nil
	}

	classNode, exists := a.packageInfo.ClassNodes[class.FullName]
	if !exists {
		return nil
	}

	return a.astHelper.ExtractMapFromClass(classNode, "casts")
}

// extractStringProperty extracts a simple string property (e.g., $table)
func (a *EloquentAnalyzer) extractStringProperty(class php.ClassInfo, propertyName string) string {
	// Get AST node for this class
	if a.packageInfo.ClassNodes == nil {
		return ""
	}

	classNode, exists := a.packageInfo.ClassNodes[class.FullName]
	if !exists {
		return ""
	}

	return a.astHelper.ExtractStringPropertyFromClass(classNode, propertyName)
}

// extractRelations detects relationship methods (hasOne, hasMany, belongsTo, etc.)
func (a *EloquentAnalyzer) extractRelations(class php.ClassInfo) []EloquentRelation {
	var relations []EloquentRelation

	// Get AST node for this class
	if a.packageInfo.ClassNodes == nil {
		return relations
	}

	classNode, exists := a.packageInfo.ClassNodes[class.FullName]
	if !exists {
		return relations
	}

	// Walk through class methods in AST
	for _, stmt := range classNode.Stmts {
		if methodNode, ok := stmt.(*ast.StmtClassMethod); ok {
			if relation := a.detectRelationFromAST(class, methodNode); relation != nil {
				relations = append(relations, *relation)
			}
		}
	}

	return relations
}

// detectRelationFromAST checks if a method AST node is a relationship definition
// and uses the enclosing class information to build fully-qualified related
// model names when possible.
func (a *EloquentAnalyzer) detectRelationFromAST(class php.ClassInfo, methodNode *ast.StmtClassMethod) *EloquentRelation {
	// Extract method calls from the method body
	calls := a.astHelper.ExtractMethodCalls(methodNode)

	for _, call := range calls {
		// Check if it's a $this->relationshipMethod() call
		if call.Object == "this" {
			relationType := ""

			switch call.Method {
			case "hasOne", "hasMany", "belongsTo", "belongsToMany",
				"hasManyThrough", "morphTo", "morphMany", "morphToMany",
				"morphedByMany":
				relationType = call.Method
			default:
				continue
			}

			// Get method name from AST
			var methodName string
			if nameNode, ok := methodNode.Name.(*ast.Identifier); ok {
				methodName = string(nameNode.Value)
			}

			// Extract related model from first argument
			relatedModel := ""
			foreignKey := ""
			localKey := ""

			if len(call.Args) > 0 {
				// First arg is usually Model::class. We normalize to a fully qualified
				// class name when possible so that downstream tools can reason about
				// relations more easily.
				relatedModel = strings.TrimSuffix(call.Args[0], "::class")
				relatedModel = strings.TrimPrefix(relatedModel, "\\")

				// If we only have a short class name (e.g. "Role")
				if relatedModel != "" && !strings.Contains(relatedModel, "\\") {
					// Check imports first
					if fullClass, ok := class.Imports[relatedModel]; ok {
						relatedModel = fullClass
					} else if class.Namespace != "" && class.Namespace != "global" {
						// Fallback to current namespace
						relatedModel = class.Namespace + "\\" + relatedModel
					}
				}
			}

			if len(call.Args) > 1 {
				foreignKey = call.Args[1]
			}

			if len(call.Args) > 2 {
				localKey = call.Args[2]
			}

			return &EloquentRelation{
				Name:         methodName,
				Type:         relationType,
				RelatedModel: relatedModel,
				ForeignKey:   foreignKey,
				LocalKey:     localKey,
			}
		}
	}

	return nil
}

// extractScopes detects query scopes (methods starting with "scope")
func (a *EloquentAnalyzer) extractScopes(class php.ClassInfo) []EloquentScope {
	var scopes []EloquentScope

	for _, method := range class.Methods {
		if strings.HasPrefix(method.Name, "scope") && len(method.Name) > 5 {
			scopeName := strings.ToLower(string(method.Name[5])) + method.Name[6:]
			scopes = append(scopes, EloquentScope{
				Name:        scopeName,
				MethodName:  method.Name,
				Description: method.Description,
				StartLine:   method.StartLine,
				EndLine:     method.EndLine,
			})
		}
	}

	return scopes
}

// extractAttributes detects accessors and mutators
func (a *EloquentAnalyzer) extractAttributes(class php.ClassInfo) []EloquentAttribute {
	var attributes []EloquentAttribute

	for _, method := range class.Methods {
		if attr := a.detectAttribute(method); attr != nil {
			attributes = append(attributes, *attr)
		}
	}

	return attributes
}

// detectAttribute checks if a method is an accessor or mutator
func (a *EloquentAnalyzer) detectAttribute(method php.MethodInfo) *EloquentAttribute {
	name := method.Name

	// Accessor pattern: getXxxAttribute
	if strings.HasPrefix(name, "get") && strings.HasSuffix(name, "Attribute") && len(name) > 12 {
		attrName := name[3 : len(name)-9] // Remove "get" and "Attribute"
		return &EloquentAttribute{
			Name:        a.snakeCase(attrName),
			MethodName:  name,
			Type:        "accessor",
			Description: method.Description,
			StartLine:   method.StartLine,
			EndLine:     method.EndLine,
		}
	}

	// Mutator pattern: setXxxAttribute
	if strings.HasPrefix(name, "set") && strings.HasSuffix(name, "Attribute") && len(name) > 12 {
		attrName := name[3 : len(name)-9] // Remove "set" and "Attribute"
		return &EloquentAttribute{
			Name:        a.snakeCase(attrName),
			MethodName:  name,
			Type:        "mutator",
			Description: method.Description,
			StartLine:   method.StartLine,
			EndLine:     method.EndLine,
		}
	}

	return nil
}

// snakeCase converts CamelCase to snake_case
func (a *EloquentAnalyzer) snakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}
