package nextjs

import (
	"testing"
)

func TestAnalyzer_DataFunctions_SSR(t *testing.T) {
	source := `
import { GetServerSideProps } from 'next';

export async function getServerSideProps(context) {
    const res = await fetch('https://api.example.com/data');
    const data = await res.json();
    return { props: { data } };
}

export default function Page({ data }) {
    return <div>{data.title}</div>;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "pages/index.tsx")

	if len(info.DataFuncs) < 1 {
		t.Fatalf("expected at least 1 data function, got %d", len(info.DataFuncs))
	}
	if info.DataFuncs[0].Name != "getServerSideProps" {
		t.Errorf("expected 'getServerSideProps', got '%s'", info.DataFuncs[0].Name)
	}
	if info.DataFuncs[0].Type != "ssr" {
		t.Errorf("expected type 'ssr', got '%s'", info.DataFuncs[0].Type)
	}
}

func TestAnalyzer_DataFunctions_SSG(t *testing.T) {
	source := `
export async function getStaticProps() {
    return { props: { data: [] } };
}

export async function getStaticPaths() {
    return { paths: [], fallback: false };
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "pages/blog/[slug].tsx")

	if len(info.DataFuncs) != 2 {
		t.Fatalf("expected 2 data functions, got %d", len(info.DataFuncs))
	}

	foundProps := false
	foundPaths := false
	for _, df := range info.DataFuncs {
		if df.Name == "getStaticProps" && df.Type == "ssg" {
			foundProps = true
		}
		if df.Name == "getStaticPaths" && df.Type == "ssg" {
			foundPaths = true
		}
	}
	if !foundProps {
		t.Error("expected getStaticProps")
	}
	if !foundPaths {
		t.Error("expected getStaticPaths")
	}
}

func TestAnalyzer_AppRouter_Metadata(t *testing.T) {
	source := `
export async function generateMetadata({ params }) {
    return { title: params.slug };
}

export async function generateStaticParams() {
    return [{ slug: 'hello' }];
}

export default function Page() {
    return <div>Hello</div>;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app/blog/[slug]/page.tsx")

	if !info.IsAppRouter {
		t.Error("expected IsAppRouter=true")
	}

	if len(info.DataFuncs) < 2 {
		t.Fatalf("expected at least 2 data functions, got %d", len(info.DataFuncs))
	}

	foundMeta := false
	foundStatic := false
	for _, df := range info.DataFuncs {
		if df.Name == "generateMetadata" && df.Type == "metadata" {
			foundMeta = true
		}
		if df.Name == "generateStaticParams" && df.Type == "ssg" {
			foundStatic = true
		}
	}
	if !foundMeta {
		t.Error("expected generateMetadata")
	}
	if !foundStatic {
		t.Error("expected generateStaticParams")
	}
}

func TestAnalyzer_PageDetection(t *testing.T) {
	tests := []struct {
		filePath   string
		expectPage bool
		route      string
		isDynamic  bool
	}{
		{"pages/index.tsx", true, "/", false},
		{"pages/about.tsx", true, "/about", false},
		{"pages/blog/[slug].tsx", true, "/blog/[slug]", true},
		{"app/page.tsx", true, "/", false},
		{"app/blog/[slug]/page.tsx", true, "/blog/[slug]", true},
	}

	analyzer := NewAnalyzer()
	for _, tt := range tests {
		info := analyzer.Analyze("", tt.filePath)
		if tt.expectPage && len(info.Pages) == 0 {
			t.Errorf("expected page for %s", tt.filePath)
			continue
		}
		if !tt.expectPage && len(info.Pages) > 0 {
			t.Errorf("did not expect page for %s", tt.filePath)
			continue
		}
		if tt.expectPage {
			page := info.Pages[0]
			if page.Route != tt.route {
				t.Errorf("file %s: expected route '%s', got '%s'", tt.filePath, tt.route, page.Route)
			}
			if page.IsDynamic != tt.isDynamic {
				t.Errorf("file %s: expected isDynamic=%v, got %v", tt.filePath, tt.isDynamic, page.IsDynamic)
			}
		}
	}
}

func TestAnalyzer_APIRoutes_AppRouter(t *testing.T) {
	source := `
export async function GET(request) {
    return Response.json({ hello: 'world' });
}

export async function POST(request) {
    const body = await request.json();
    return Response.json(body);
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app/api/users/route.ts")

	if len(info.APIRoutes) < 2 {
		t.Fatalf("expected at least 2 API routes, got %d", len(info.APIRoutes))
	}

	methods := make(map[string]bool)
	for _, r := range info.APIRoutes {
		for _, m := range r.Methods {
			methods[m] = true
		}
	}
	if !methods["GET"] {
		t.Error("expected GET handler")
	}
	if !methods["POST"] {
		t.Error("expected POST handler")
	}
}

func TestAnalyzer_APIRoutes_PagesRouter(t *testing.T) {
	source := `
export default function handler(req, res) {
    res.status(200).json({ name: 'John' });
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "pages/api/users/index.ts")

	if len(info.APIRoutes) < 1 {
		t.Fatalf("expected at least 1 API route, got %d", len(info.APIRoutes))
	}
	if info.APIRoutes[0].Route != "/api/users" {
		t.Errorf("expected route '/api/users', got '%s'", info.APIRoutes[0].Route)
	}
}

func TestAnalyzer_Middleware(t *testing.T) {
	source := `
import { NextResponse } from 'next/server';

export function middleware(request) {
    return NextResponse.next();
}

export const config = {
    matcher: ['/dashboard/:path*'],
};
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "middleware.ts")

	if len(info.Middleware) != 1 {
		t.Fatalf("expected 1 middleware, got %d", len(info.Middleware))
	}
	if len(info.Middleware[0].Matchers) < 1 {
		t.Errorf("expected at least 1 matcher, got %d", len(info.Middleware[0].Matchers))
	}
}

func TestAnalyzer_Layout(t *testing.T) {
	source := `
export default function RootLayout({ children }) {
    return <html><body>{children}</body></html>;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app/layout.tsx")

	if len(info.Layouts) != 1 {
		t.Fatalf("expected 1 layout, got %d", len(info.Layouts))
	}
	if info.Layouts[0].Route != "/" {
		t.Errorf("expected route '/', got '%s'", info.Layouts[0].Route)
	}
}

func TestIsNextJSFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"pages/index.tsx", true},
		{"app/page.tsx", true},
		{"middleware.ts", true},
		{"src/utils.ts", false},
		{"components/Button.tsx", false},
	}

	for _, tt := range tests {
		result := IsNextJSFile(tt.path)
		if result != tt.expected {
			t.Errorf("IsNextJSFile(%q) = %v, want %v", tt.path, result, tt.expected)
		}
	}
}

func TestIsNextJSProject(t *testing.T) {
	tests := []struct {
		source   string
		expected bool
	}{
		{`import Image from 'next/image';`, true},
		{`import Link from 'next/link';`, true},
		{`import { useRouter } from 'next/router';`, true},
		{`import React from 'react';`, false},
	}

	for _, tt := range tests {
		result := IsNextJSProject(tt.source)
		if result != tt.expected {
			t.Errorf("IsNextJSProject(...) = %v, want %v", result, tt.expected)
		}
	}
}
