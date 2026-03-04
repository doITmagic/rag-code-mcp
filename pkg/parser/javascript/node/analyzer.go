package node

import (
	"regexp"
	"strings"
)

var (
	// Express routes: app.get('/path', handler) or router.get('/path', handler)
	reExpressRoute = regexp.MustCompile(`(?m)(?:app|router|server)\.(get|post|put|delete|patch|all|use)\s*\(\s*['"]([^'"]*)['"]\s*(?:,\s*(\w+))?\s*`)

	// Middleware: app.use(middleware)
	reMiddleware = regexp.MustCompile(`(?m)(?:app|router)\.(use)\s*\(\s*(\w+(?:\.\w+)*)\s*\(?\s*`)

	// require() calls
	reRequire = regexp.MustCompile(`(?m)(?:const|let|var)\s+(\{[^}]+\}|\w+)\s*=\s*require\s*\(\s*['"]([^'"]+)['"]\s*\)`)

	// module.exports
	reModuleExports = regexp.MustCompile(`(?m)^module\.exports\s*=\s*(\w+|{|class|function)`)
	reExportsDot    = regexp.MustCompile(`(?m)^exports\.(\w+)\s*=`)

	// Router creation
	reRouterCreate = regexp.MustCompile(`(?m)(?:const|let|var)\s+\w+\s*=\s*(?:express\.)?Router\s*\(\s*\)`)

	// Common built-in middleware
	builtinMiddleware = map[string]bool{
		"express.json":          true,
		"express.urlencoded":    true,
		"express.static":        true,
		"cors":                  true,
		"helmet":                true,
		"morgan":                true,
		"bodyParser.json":       true,
		"bodyParser.urlencoded": true,
		"cookieParser":          true,
		"compression":           true,
	}
)

// Analyzer detects Node.js/Express-specific patterns
type Analyzer struct{}

// NewAnalyzer creates a new Node.js analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// IsNodeProject checks if a file looks like a Node.js project file
func IsNodeProject(source string) bool {
	return reRequire.MatchString(source) ||
		strings.Contains(source, "module.exports") ||
		strings.Contains(source, "express()")
}

// Analyze performs Node.js-specific analysis
func (a *Analyzer) Analyze(source string, filePath string) *NodeInfo {
	info := &NodeInfo{}

	info.Routes = a.detectRoutes(source, filePath)
	info.Middleware = a.detectMiddleware(source, filePath)
	info.Requires = a.detectRequires(source)
	info.ModuleExports = a.detectModuleExports(source, filePath)

	return info
}

// detectRoutes finds Express route definitions
func (a *Analyzer) detectRoutes(source string, filePath string) []Route {
	var routes []Route

	for _, match := range reExpressRoute.FindAllStringSubmatchIndex(source, -1) {
		method := source[match[2]:match[3]]
		path := source[match[4]:match[5]]
		handler := ""
		if match[6] != -1 {
			handler = source[match[6]:match[7]]
		}

		line := strings.Count(source[:match[0]], "\n") + 1
		isRouter := strings.Contains(source[max(0, match[0]-20):match[0]+10], "router.")

		routes = append(routes, Route{
			Method:   method,
			Path:     path,
			Handler:  handler,
			IsRouter: isRouter,
			FilePath: filePath,
			Line:     line,
		})
	}

	return routes
}

// detectMiddleware finds Express middleware usage
func (a *Analyzer) detectMiddleware(source string, filePath string) []Middleware {
	var middleware []Middleware
	seen := make(map[string]bool)

	for _, match := range reMiddleware.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[4]:match[5]]
		line := strings.Count(source[:match[0]], "\n") + 1

		if seen[name] {
			continue
		}
		seen[name] = true

		middleware = append(middleware, Middleware{
			Name:     name,
			IsCustom: !builtinMiddleware[name],
			FilePath: filePath,
			Line:     line,
		})
	}

	return middleware
}

// detectRequires finds require() calls
func (a *Analyzer) detectRequires(source string) []Require {
	var requires []Require

	for _, match := range reRequire.FindAllStringSubmatchIndex(source, -1) {
		binding := source[match[2]:match[3]]
		module := source[match[4]:match[5]]
		line := strings.Count(source[:match[0]], "\n") + 1

		requires = append(requires, Require{
			Module:  module,
			Binding: binding,
			IsLocal: strings.HasPrefix(module, "."),
			Line:    line,
		})
	}

	return requires
}

// detectModuleExports finds module.exports assignments
func (a *Analyzer) detectModuleExports(source string, filePath string) []ModuleExport {
	var exports []ModuleExport

	// module.exports = X
	for _, match := range reModuleExports.FindAllStringSubmatchIndex(source, -1) {
		value := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1

		exportType := "value"
		switch {
		case value == "function":
			exportType = "function"
		case value == "class":
			exportType = "class"
		case value == "{":
			exportType = "object"
		}

		exports = append(exports, ModuleExport{
			Name:     value,
			Type:     exportType,
			FilePath: filePath,
			Line:     line,
		})
	}

	// exports.X = ...
	for _, match := range reExportsDot.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1

		exports = append(exports, ModuleExport{
			Name:     name,
			Type:     "property",
			FilePath: filePath,
			Line:     line,
		})
	}

	return exports
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
