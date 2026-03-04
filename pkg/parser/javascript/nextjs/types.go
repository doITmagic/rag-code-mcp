package nextjs

// NextJSInfo contains Next.js-specific analysis results
type NextJSInfo struct {
	Pages       []Page       `json:"pages,omitempty"`
	APIRoutes   []APIRoute   `json:"api_routes,omitempty"`
	Middleware  []Middleware `json:"middleware,omitempty"`
	Layouts     []Layout     `json:"layouts,omitempty"`
	DataFuncs   []DataFunc   `json:"data_funcs,omitempty"` // getServerSideProps, etc.
	IsAppRouter bool         `json:"is_app_router"`        // app/ vs pages/
}

// Page represents a Next.js page component
type Page struct {
	Name       string `json:"name"`
	Route      string `json:"route"`                // /about, /blog/[slug]
	IsDynamic  bool   `json:"is_dynamic,omitempty"` // [slug], [...params]
	IsLayout   bool   `json:"is_layout,omitempty"`
	IsLoading  bool   `json:"is_loading,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	IsNotFound bool   `json:"is_not_found,omitempty"`
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
}

// APIRoute represents a Next.js API route
type APIRoute struct {
	Route    string   `json:"route"`
	Methods  []string `json:"methods,omitempty"` // GET, POST, etc. (app router)
	FilePath string   `json:"file_path"`
	Line     int      `json:"line"`
}

// Middleware represents Next.js middleware
type Middleware struct {
	Matchers []string `json:"matchers,omitempty"`
	FilePath string   `json:"file_path"`
	Line     int      `json:"line"`
}

// Layout represents a Next.js layout component (app router)
type Layout struct {
	Name     string `json:"name"`
	Route    string `json:"route"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// DataFunc represents a Next.js data fetching function
type DataFunc struct {
	Name     string `json:"name"` // getServerSideProps, getStaticProps, generateMetadata, etc.
	Type     string `json:"type"` // ssr, ssg, isr, metadata
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}
