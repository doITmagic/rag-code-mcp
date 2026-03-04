package nextjs

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Next.js data fetching functions (pages router)
	reGetServerSideProps = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+getServerSideProps\s*\(`)
	reGetStaticProps     = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+getStaticProps\s*\(`)
	reGetStaticPaths     = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+getStaticPaths\s*\(`)
	reGetInitialProps    = regexp.MustCompile(`(?m)\w+\.getInitialProps\s*=`)

	// App router patterns
	reGenerateMetadata = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+generateMetadata\s*\(`)
	reGenerateStaticP  = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+generateStaticParams\s*\(`)

	// API route handlers (app router)
	reAPIHandler = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\s*\(`)

	// Middleware config
	reMiddlewareMatcher = regexp.MustCompile(`(?m)export\s+const\s+config\s*=\s*\{[^}]*matcher\s*:\s*\[?['"]([^'"]+)['"]`)

	// Next.js imports
	reNextImport = regexp.MustCompile(`(?m)^import\s+.*\s+from\s+['"]next/`)
	reNextRouter = regexp.MustCompile(`(?m)^import\s+.*\s+from\s+['"]next/(?:router|navigation)['"]`)
	reNextImage  = regexp.MustCompile(`\bImage\b.*from\s+['"]next/image['"]`)
	reNextLink   = regexp.MustCompile(`\bLink\b.*from\s+['"]next/link['"]`)

	// Special file names (app router)
	appRouterFiles = map[string]string{
		"page":      "page",
		"layout":    "layout",
		"loading":   "loading",
		"error":     "error",
		"not-found": "not-found",
		"route":     "api_route",
		"template":  "template",
		"default":   "default",
	}
)

// Analyzer detects Next.js specific patterns
type Analyzer struct{}

// NewAnalyzer creates a new Next.js analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// IsNextJSFile checks if a file belongs to a Next.js project
func IsNextJSFile(filePath string) bool {
	normalized := filepath.ToSlash(filePath)
	base := filepath.Base(filePath)
	return strings.Contains(normalized, "/pages/") || strings.HasPrefix(normalized, "pages/") ||
		strings.Contains(normalized, "/app/") || strings.HasPrefix(normalized, "app/") ||
		strings.HasPrefix(base, "middleware.")
}

// IsNextJSProject checks source for Next.js imports
func IsNextJSProject(source string) bool {
	return reNextImport.MatchString(source)
}

// Analyze performs Next.js-specific analysis
func (a *Analyzer) Analyze(source string, filePath string) *NextJSInfo {
	info := &NextJSInfo{}
	normalized := filepath.ToSlash(filePath)

	// Determine if app/ or pages/ router
	info.IsAppRouter = strings.Contains(normalized, "/app/") || strings.HasPrefix(normalized, "app/")

	// Detect data functions
	info.DataFuncs = a.detectDataFunctions(source, filePath)

	// Detect pages from file path
	page := a.detectPage(filePath)
	if page != nil {
		info.Pages = append(info.Pages, *page)
	}

	// Detect API routes
	info.APIRoutes = a.detectAPIRoutes(source, filePath)

	// Detect middleware
	info.Middleware = a.detectMiddleware(source, filePath)

	// Detect layouts (app router)
	if info.IsAppRouter {
		layout := a.detectLayout(filePath)
		if layout != nil {
			info.Layouts = append(info.Layouts, *layout)
		}
	}

	return info
}

// detectDataFunctions finds Next.js data fetching functions
func (a *Analyzer) detectDataFunctions(source string, filePath string) []DataFunc {
	var funcs []DataFunc

	type pattern struct {
		re   *regexp.Regexp
		name string
		typ  string
	}

	patterns := []pattern{
		{reGetServerSideProps, "getServerSideProps", "ssr"},
		{reGetStaticProps, "getStaticProps", "ssg"},
		{reGetStaticPaths, "getStaticPaths", "ssg"},
		{reGenerateMetadata, "generateMetadata", "metadata"},
		{reGenerateStaticP, "generateStaticParams", "ssg"},
	}

	for _, p := range patterns {
		if loc := p.re.FindStringIndex(source); loc != nil {
			line := strings.Count(source[:loc[0]], "\n") + 1
			funcs = append(funcs, DataFunc{
				Name:     p.name,
				Type:     p.typ,
				FilePath: filePath,
				Line:     line,
			})
		}
	}

	// getInitialProps (legacy)
	if loc := reGetInitialProps.FindStringIndex(source); loc != nil {
		line := strings.Count(source[:loc[0]], "\n") + 1
		funcs = append(funcs, DataFunc{
			Name:     "getInitialProps",
			Type:     "legacy",
			FilePath: filePath,
			Line:     line,
		})
	}

	return funcs
}

// detectPage derives page info from file path
func (a *Analyzer) detectPage(filePath string) *Page {
	normalized := filepath.ToSlash(filePath)

	// Extract route from file path
	var route string
	var inPages, inApp bool

	if idx := strings.Index(normalized, "/pages/"); idx != -1 {
		route = normalized[idx+len("/pages/"):]
		inPages = true
	} else if strings.HasPrefix(normalized, "pages/") {
		route = strings.TrimPrefix(normalized, "pages/")
		inPages = true
	} else if idx := strings.Index(normalized, "/app/"); idx != -1 {
		route = normalized[idx+len("/app/"):]
		inApp = true
	} else if strings.HasPrefix(normalized, "app/") {
		route = strings.TrimPrefix(normalized, "app/")
		inApp = true
	} else {
		return nil
	}

	// Strip extension
	ext := filepath.Ext(route)
	route = strings.TrimSuffix(route, ext)

	if inApp {
		// App router: only page.tsx/page.js files are actual pages
		baseName := filepath.Base(route)
		dir := filepath.Dir(route)

		if _, isSpecial := appRouterFiles[baseName]; !isSpecial {
			return nil
		}

		if baseName == "layout" || baseName == "loading" ||
			baseName == "error" || baseName == "not-found" ||
			baseName == "template" || baseName == "default" || baseName == "route" {
			return nil // handled separately
		}

		// page.tsx → route is the directory
		route = "/" + filepath.ToSlash(dir)
		if route == "/." {
			route = "/"
		}
	} else if inPages {
		// Pages router: index.tsx → /
		if route == "index" {
			route = ""
		} else if strings.HasSuffix(route, "/index") {
			route = strings.TrimSuffix(route, "/index")
		}
		// Strip api/ for API routes
		if strings.HasPrefix(route, "api/") {
			return nil // handled as API route
		}
		route = "/" + route
	}

	// Clean up trailing slashes (except root)
	if route != "/" {
		route = strings.TrimSuffix(route, "/")
	}

	isDynamic := strings.Contains(route, "[")

	return &Page{
		Name:      filepath.Base(filePath),
		Route:     route,
		IsDynamic: isDynamic,
		FilePath:  filePath,
	}
}

// detectAPIRoutes finds API route handlers
func (a *Analyzer) detectAPIRoutes(source string, filePath string) []APIRoute {
	var routes []APIRoute
	normalized := filepath.ToSlash(filePath)

	// App router: export function GET/POST/etc
	for _, match := range reAPIHandler.FindAllStringSubmatchIndex(source, -1) {
		method := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1

		// Derive route from file path
		route := ""
		if idx := strings.Index(normalized, "/app/"); idx != -1 {
			route = normalized[idx+len("/app/"):]
			route = strings.TrimSuffix(route, "/route.ts")
			route = strings.TrimSuffix(route, "/route.js")
			route = "/" + route
		}

		routes = append(routes, APIRoute{
			Route:    route,
			Methods:  []string{method},
			FilePath: filePath,
			Line:     line,
		})
	}

	// Pages router: /pages/api/... with default export handler
	if strings.Contains(normalized, "/pages/api/") || strings.HasPrefix(normalized, "pages/api/") {
		route := ""
		if idx := strings.Index(normalized, "/pages"); idx != -1 {
			route = normalized[idx+len("/pages"):]
		} else if strings.HasPrefix(normalized, "pages") {
			route = normalized[len("pages"):]
		}
		if route != "" {
			ext := filepath.Ext(route)
			route = strings.TrimSuffix(route, ext)
			if strings.HasSuffix(route, "/index") {
				route = strings.TrimSuffix(route, "/index")
			}
		}
		if route != "" && len(routes) == 0 {
			routes = append(routes, APIRoute{
				Route:    route,
				FilePath: filePath,
				Line:     1,
			})
		}
	}

	return routes
}

// detectMiddleware finds Next.js middleware configuration
func (a *Analyzer) detectMiddleware(source string, filePath string) []Middleware {
	var middleware []Middleware
	baseName := filepath.Base(filePath)

	if !strings.HasPrefix(baseName, "middleware.") {
		return nil
	}

	mw := Middleware{
		FilePath: filePath,
		Line:     1,
	}

	// Extract matchers
	for _, match := range reMiddlewareMatcher.FindAllStringSubmatchIndex(source, -1) {
		matcher := source[match[2]:match[3]]
		mw.Matchers = append(mw.Matchers, matcher)
	}

	middleware = append(middleware, mw)
	return middleware
}

// detectLayout detects layout files in app router
func (a *Analyzer) detectLayout(filePath string) *Layout {
	normalized := filepath.ToSlash(filePath)
	baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	if baseName != "layout" {
		return nil
	}

	route := ""
	if idx := strings.Index(normalized, "/app/"); idx != -1 {
		route = normalized[idx+len("/app/"):]
		route = "/" + filepath.ToSlash(filepath.Dir(route))
		if route == "/." {
			route = "/"
		}
	} else if strings.HasPrefix(normalized, "app/") {
		route = strings.TrimPrefix(normalized, "app/")
		route = "/" + filepath.ToSlash(filepath.Dir(route))
		if route == "/." {
			route = "/"
		}
	}

	return &Layout{
		Name:     "layout",
		Route:    route,
		FilePath: filePath,
		Line:     1,
	}
}
