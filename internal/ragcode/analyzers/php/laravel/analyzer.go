package laravel

import (
	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
)

// Analyzer is the main Laravel framework analyzer that coordinates all Laravel-specific analyzers
type Analyzer struct {
	eloquentAnalyzer   *EloquentAnalyzer
	controllerAnalyzer *ControllerAnalyzer
	routeAnalyzer      *RouteAnalyzer
}

// NewAnalyzer creates a new Laravel framework analyzer
func NewAnalyzer(packageInfo *php.PackageInfo) *Analyzer {
	return &Analyzer{
		eloquentAnalyzer:   NewEloquentAnalyzer(packageInfo),
		controllerAnalyzer: NewControllerAnalyzer(packageInfo),
		routeAnalyzer:      NewRouteAnalyzer(),
	}
}

// Analyze performs complete Laravel framework analysis on the package
func (a *Analyzer) Analyze() *LaravelInfo {
	info := &LaravelInfo{
		Models:      a.eloquentAnalyzer.AnalyzeModels(),
		Controllers: a.controllerAnalyzer.AnalyzeControllers(),
	}

	return info
}

// AnalyzeWithRoutes performs analysis including route files
func (a *Analyzer) AnalyzeWithRoutes(routePaths []string) *LaravelInfo {
	info := a.Analyze()

	if len(routePaths) > 0 {
		routes, err := a.routeAnalyzer.Analyze(routePaths)
		if err == nil {
			info.Routes = routes
		}
	}

	return info
}
