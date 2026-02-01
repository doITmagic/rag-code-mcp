package laravel

import (
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
)

// ControllerAnalyzer detects and analyzes Laravel controllers
type ControllerAnalyzer struct {
	packageInfo *php.PackageInfo
}

// NewControllerAnalyzer creates a new controller analyzer
func NewControllerAnalyzer(pkgInfo *php.PackageInfo) *ControllerAnalyzer {
	return &ControllerAnalyzer{
		packageInfo: pkgInfo,
	}
}

// AnalyzeControllers detects controllers and extracts actions
func (a *ControllerAnalyzer) AnalyzeControllers() []Controller {
	var controllers []Controller

	for _, class := range a.packageInfo.Classes {
		if a.isController(class) {
			controller := a.extractController(class)
			controllers = append(controllers, controller)
		}
	}

	return controllers
}

// isController checks if a class is a Laravel controller
func (a *ControllerAnalyzer) isController(class php.ClassInfo) bool {
	// Check if extends Controller or is in Controllers namespace
	if strings.Contains(class.Namespace, "Controllers") {
		return true
	}

	if class.Extends == "" {
		return false
	}

	extends := class.Extends
	return extends == "Controller" ||
		extends == "BaseController" ||
		extends == "ApiController" ||
		strings.HasSuffix(extends, "\\Controller")
}

// extractController extracts controller information
func (a *ControllerAnalyzer) extractController(class php.ClassInfo) Controller {
	controller := Controller{
		ClassName:      class.Name,
		Namespace:      class.Namespace,
		FullName:       class.FullName,
		Description:    class.Description,
		BaseController: class.Extends,
		IsApi:          a.isApiController(class),
		IsResource:     a.isResourceController(class),
		FilePath:       class.FilePath,
		StartLine:      class.StartLine,
		EndLine:        class.EndLine,
	}

	// Extract actions
	controller.Actions = a.extractActions(class)

	// Extract middleware (would need AST parsing of constructor or middleware() method)
	controller.Middleware = a.extractMiddleware(class)

	return controller
}

// isApiController checks if controller is API-specific
func (a *ControllerAnalyzer) isApiController(class php.ClassInfo) bool {
	return strings.Contains(class.Namespace, "\\Api") ||
		strings.Contains(class.Namespace, "\\API") ||
		strings.HasSuffix(class.Name, "ApiController") ||
		strings.Contains(class.FilePath, "/Api/") ||
		strings.Contains(class.FilePath, "/API/")
}

// isResourceController checks for standard resource methods
func (a *ControllerAnalyzer) isResourceController(class php.ClassInfo) bool {
	resourceMethods := map[string]bool{
		"index":   false,
		"create":  false,
		"store":   false,
		"show":    false,
		"edit":    false,
		"update":  false,
		"destroy": false,
	}

	for _, method := range class.Methods {
		if _, exists := resourceMethods[method.Name]; exists {
			resourceMethods[method.Name] = true
		}
	}

	// If has at least 4 resource methods, consider it a resource controller
	count := 0
	for _, found := range resourceMethods {
		if found {
			count++
		}
	}

	return count >= 4
}

// extractActions extracts controller actions
func (a *ControllerAnalyzer) extractActions(class php.ClassInfo) []ControllerAction {
	var actions []ControllerAction

	for _, method := range class.Methods {
		// Skip magic methods and non-public methods
		if strings.HasPrefix(method.Name, "__") || method.Visibility != "public" {
			continue
		}

		action := ControllerAction{
			Name:        method.Name,
			Description: method.Description,
			Parameters:  a.extractParameterNames(method),
			Returns:     method.ReturnType,
			StartLine:   method.StartLine,
			EndLine:     method.EndLine,
		}

		// Detect HTTP methods from method name
		action.HttpMethods = a.detectHttpMethods(method.Name)

		actions = append(actions, action)
	}

	return actions
}

// extractParameterNames extracts parameter names from method
func (a *ControllerAnalyzer) extractParameterNames(method php.MethodInfo) []string {
	var params []string
	for _, param := range method.Parameters {
		// Remove $ prefix from parameter names
		paramName := param.Name
		if len(paramName) > 0 && paramName[0] == '$' {
			paramName = paramName[1:]
		}
		params = append(params, paramName)
	}
	return params
}

// detectHttpMethods tries to infer HTTP methods from action name
func (a *ControllerAnalyzer) detectHttpMethods(actionName string) []string {
	lower := strings.ToLower(actionName)

	// Standard resource methods
	switch lower {
	case "index", "show", "create", "edit":
		return []string{"GET"}
	case "store":
		return []string{"POST"}
	case "update":
		return []string{"PUT", "PATCH"}
	case "destroy":
		return []string{"DELETE"}
	}

	// Method name hints
	if strings.HasPrefix(lower, "get") || strings.HasPrefix(lower, "show") || strings.HasPrefix(lower, "list") {
		return []string{"GET"}
	}
	if strings.HasPrefix(lower, "create") || strings.HasPrefix(lower, "store") {
		return []string{"POST"}
	}
	if strings.HasPrefix(lower, "update") {
		return []string{"PUT", "PATCH"}
	}
	if strings.HasPrefix(lower, "delete") || strings.HasPrefix(lower, "destroy") {
		return []string{"DELETE"}
	}

	return []string{}
}

// extractMiddleware extracts middleware from controller
func (a *ControllerAnalyzer) extractMiddleware(class php.ClassInfo) []string {
	// TODO: Parse constructor or middleware() method to extract middleware calls
	// This requires AST parsing of method bodies
	return nil
}
