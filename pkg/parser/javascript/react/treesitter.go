package react

import (
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// TreeSitterAnalyzer uses tree-sitter AST for accurate React/RN pattern detection.
// Caches Parser instances per language to avoid re-allocating expensive lookup tables.
type TreeSitterAnalyzer struct {
	mu      sync.Mutex
	parsers map[string]*gotreesitter.Parser
}

// NewTreeSitterAnalyzer creates a new tree-sitter based React analyzer
func NewTreeSitterAnalyzer() *TreeSitterAnalyzer {
	return &TreeSitterAnalyzer{
		parsers: make(map[string]*gotreesitter.Parser),
	}
}

func (t *TreeSitterAnalyzer) getOrCreateParser(lang *grammars.LangEntry) *gotreesitter.Parser {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cached, ok := t.parsers[lang.Name]; ok {
		return cached
	}
	p := gotreesitter.NewParser(lang.Language())
	t.parsers[lang.Name] = p
	return p
}

// ReleaseResources drops cached tree-sitter parsers so the GC can reclaim arena memory.
func (t *TreeSitterAnalyzer) ReleaseResources() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parsers = make(map[string]*gotreesitter.Parser)
}

// Analyze parses source with tree-sitter and extracts React/RN patterns from the AST
func (t *TreeSitterAnalyzer) Analyze(source []byte, filePath string) *ReactInfo {
	lang := grammars.DetectLanguage(filePath)
	if lang == nil {
		return nil
	}

	parser := t.getOrCreateParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return nil
	}
	defer tree.Release()

	root := tree.RootNode()
	langObj := lang.Language()

	info := &ReactInfo{}
	info.IsReactNative = t.hasRNImport(root, source, langObj)

	isReact := t.hasReactImport(root, source, langObj) || t.hasJSXInTree(root, source, langObj)
	if !isReact && !info.IsReactNative {
		return nil
	}

	// Walk AST for all patterns
	info.Components = t.detectComponents(root, source, langObj, filePath)
	info.Hooks = t.detectHooks(root, source, langObj, filePath)

	// Custom hook definitions (functions named useXxx) also get added as hooks
	info.Hooks = append(info.Hooks, t.detectCustomHookDefinitions(root, source, langObj, filePath)...)

	info.Contexts = t.detectContexts(root, source, langObj, filePath)

	// Mark RN-specific component properties
	if info.IsReactNative {
		for i := range info.Components {
			info.Components[i].IsNative = true
			if strings.Contains(filePath, ".ios.") {
				info.Components[i].Platform = "ios"
			} else if strings.Contains(filePath, ".android.") {
				info.Components[i].Platform = "android"
			}
			name := info.Components[i].Name
			if strings.HasSuffix(name, "Screen") || strings.Contains(filePath, "screens/") {
				info.Components[i].IsScreen = true
			}
		}

		info.NativeStyles = t.detectStyleSheets(root, source, langObj, filePath)
		info.Screens = t.detectScreens(root, source, langObj, filePath)
		info.Navigation = t.detectNavigators(root, source, langObj, filePath)
		info.NativeModules = t.detectNativeModules(root, source, langObj, filePath)
	}

	return info
}

// --- Import detection ---

func (t *TreeSitterAnalyzer) hasReactImport(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language) bool {
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Type(lang) == "import_statement" {
			text := child.Text(source)
			if strings.Contains(text, "'react'") || strings.Contains(text, "\"react\"") {
				return true
			}
		}
	}
	return false
}

func (t *TreeSitterAnalyzer) hasRNImport(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language) bool {
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Type(lang) == "import_statement" {
			text := child.Text(source)
			if strings.Contains(text, "'react-native'") || strings.Contains(text, "\"react-native\"") ||
				strings.Contains(text, "@react-navigation/") {
				return true
			}
		}
	}
	return false
}

func (t *TreeSitterAnalyzer) hasJSXInTree(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language) bool {
	return t.findNodeRecursive(root, lang, func(node *gotreesitter.Node) bool {
		nodeType := node.Type(lang)
		return nodeType == "jsx_element" || nodeType == "jsx_self_closing_element" || nodeType == "jsx_fragment"
	})
}

// --- Component detection ---

func (t *TreeSitterAnalyzer) detectComponents(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Component {
	var components []Component

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(lang)

		switch nodeType {
		case "function_declaration":
			comp := t.checkFunctionComponent(child, source, lang, filePath, false, false)
			if comp != nil {
				components = append(components, *comp)
			}

		case "export_statement":
			comps := t.processExportForComponents(child, source, lang, filePath)
			components = append(components, comps...)

		case "lexical_declaration", "variable_declaration":
			comp := t.checkArrowComponent(child, source, lang, filePath, false, false)
			if comp != nil {
				components = append(components, *comp)
			}

		case "class_declaration":
			comp := t.checkClassComponent(child, source, lang, filePath)
			if comp != nil {
				components = append(components, *comp)
			}
		}
	}

	return components
}

func (t *TreeSitterAnalyzer) checkFunctionComponent(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, exported, isDefault bool) *Component {
	var name string
	var params []string

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Type(lang) {
		case "identifier":
			name = child.Text(source)
		case "formal_parameters":
			params = t.extractDestructuredProps(child, source, lang)
		}
	}

	// React components start with uppercase
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return nil
	}

	// Check if body contains JSX
	hasJSX := t.findNodeRecursive(node, lang, func(n *gotreesitter.Node) bool {
		nt := n.Type(lang)
		return nt == "jsx_element" || nt == "jsx_self_closing_element" || nt == "jsx_fragment"
	})
	if !hasJSX {
		return nil
	}

	// Extract hooks used in the function body
	hooks := t.extractHookNamesFromNode(node, source, lang)

	return &Component{
		Name:       name,
		Type:       "functional",
		Props:      params,
		Hooks:      hooks,
		IsExported: exported,
		IsDefault:  isDefault,
		HasJSX:     true,
		FilePath:   filePath,
		StartLine:  int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
	}
}

func (t *TreeSitterAnalyzer) checkArrowComponent(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, exported, isDefault bool) *Component {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Type(lang) != "variable_declarator" {
			continue
		}

		var name string
		var arrowNode *gotreesitter.Node

		for j := 0; j < child.ChildCount(); j++ {
			gc := child.Child(j)
			switch gc.Type(lang) {
			case "identifier":
				name = gc.Text(source)
			case "arrow_function":
				arrowNode = gc
			}
		}

		if name == "" || name[0] < 'A' || name[0] > 'Z' || arrowNode == nil {
			continue
		}

		// Check for JSX in the arrow function
		hasJSX := t.findNodeRecursive(arrowNode, lang, func(n *gotreesitter.Node) bool {
			nt := n.Type(lang)
			return nt == "jsx_element" || nt == "jsx_self_closing_element" || nt == "jsx_fragment"
		})
		if !hasJSX {
			continue
		}

		// Extract props from arrow function params
		var props []string
		for j := 0; j < arrowNode.ChildCount(); j++ {
			gc := arrowNode.Child(j)
			if gc.Type(lang) == "formal_parameters" || gc.Type(lang) == "object_pattern" {
				props = t.extractDestructuredProps(gc, source, lang)
			}
		}

		hooks := t.extractHookNamesFromNode(arrowNode, source, lang)

		return &Component{
			Name:       name,
			Type:       "functional",
			Props:      props,
			Hooks:      hooks,
			IsExported: exported,
			IsDefault:  isDefault,
			HasJSX:     true,
			FilePath:   filePath,
			StartLine:  int(node.StartPoint().Row) + 1,
			EndLine:    int(node.EndPoint().Row) + 1,
		}
	}

	return nil
}

func (t *TreeSitterAnalyzer) checkClassComponent(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *Component {
	var name string
	isReactClass := false

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Type(lang) {
		case "identifier", "type_identifier":
			if name == "" {
				name = child.Text(source)
			}
		case "class_heritage":
			text := child.Text(source)
			if strings.Contains(text, "Component") || strings.Contains(text, "PureComponent") {
				isReactClass = true
			}
		}
	}

	if !isReactClass || name == "" {
		return nil
	}

	return &Component{
		Name:      name,
		Type:      "class",
		HasJSX:    true,
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}
}

func (t *TreeSitterAnalyzer) processExportForComponents(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Component {
	var components []Component
	isDefault := false

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		if childType == "default" {
			isDefault = true
			continue
		}

		switch childType {
		case "function_declaration":
			comp := t.checkFunctionComponent(child, source, lang, filePath, true, isDefault)
			if comp != nil {
				components = append(components, *comp)
			}
		case "lexical_declaration":
			comp := t.checkArrowComponent(child, source, lang, filePath, true, isDefault)
			if comp != nil {
				components = append(components, *comp)
			}
		case "class_declaration":
			comp := t.checkClassComponent(child, source, lang, filePath)
			if comp != nil {
				comp.IsExported = true
				comp.IsDefault = isDefault
				components = append(components, *comp)
			}
		}
	}

	return components
}

// --- Hook detection ---

func (t *TreeSitterAnalyzer) detectHooks(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []HookUsage {
	var hooks []HookUsage

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "call_expression" {
			return
		}

		// Get the function name
		callee := node.Child(0)
		if callee == nil {
			return
		}

		name := callee.Text(source)
		if !strings.HasPrefix(name, "use") || len(name) < 4 {
			return
		}
		// Check it starts with useX (uppercase after use)
		if name[3] < 'A' || name[3] > 'Z' {
			return
		}

		isRN := rnHooks[name]
		isCustom := !builtinHooks[name] && !isRN

		hooks = append(hooks, HookUsage{
			Name:     name,
			IsCustom: isCustom,
			IsRN:     isRN,
			FilePath: filePath,
			Line:     int(node.StartPoint().Row) + 1,
		})
	})

	return hooks
}

func (t *TreeSitterAnalyzer) extractHookNamesFromNode(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []string {
	seen := make(map[string]bool)
	var hooks []string

	t.walkTree(node, lang, func(n *gotreesitter.Node) {
		if n.Type(lang) != "call_expression" {
			return
		}
		callee := n.Child(0)
		if callee == nil {
			return
		}
		name := callee.Text(source)
		if strings.HasPrefix(name, "use") && len(name) > 3 && name[3] >= 'A' && name[3] <= 'Z' {
			if !seen[name] {
				seen[name] = true
				hooks = append(hooks, name)
			}
		}
	})

	return hooks
}

// detectCustomHookDefinitions finds function declarations named useXxx (custom hook definitions)
func (t *TreeSitterAnalyzer) detectCustomHookDefinitions(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []HookUsage {
	var hooks []HookUsage

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(lang)

		var name string
		var line int

		switch nodeType {
		case "function_declaration":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				if gc.Type(lang) == "identifier" {
					name = gc.Text(source)
					break
				}
			}
			line = int(child.StartPoint().Row) + 1

		case "export_statement":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				if gc.Type(lang) == "function_declaration" {
					for k := 0; k < gc.ChildCount(); k++ {
						gk := gc.Child(k)
						if gk.Type(lang) == "identifier" {
							name = gk.Text(source)
							break
						}
					}
					line = int(gc.StartPoint().Row) + 1
				}
			}
		}

		// Custom hook: starts with use + uppercase letter
		if name != "" && strings.HasPrefix(name, "use") && len(name) > 3 && name[3] >= 'A' && name[3] <= 'Z' {
			if !builtinHooks[name] && !rnHooks[name] {
				hooks = append(hooks, HookUsage{
					Name:     name,
					IsCustom: true,
					FilePath: filePath,
					Line:     line,
				})
			}
		}
	}

	return hooks
}

// --- Context detection ---

func (t *TreeSitterAnalyzer) detectContexts(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Context {
	var contexts []Context

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(lang)

		if nodeType == "lexical_declaration" || nodeType == "variable_declaration" {
			t.findContextInDeclaration(child, source, lang, filePath, &contexts)
		}
	}

	return contexts
}

func (t *TreeSitterAnalyzer) findContextInDeclaration(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, contexts *[]Context) {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Type(lang) != "variable_declarator" {
			continue
		}

		var name string
		isContext := false

		for j := 0; j < child.ChildCount(); j++ {
			gc := child.Child(j)
			switch gc.Type(lang) {
			case "identifier":
				name = gc.Text(source)
			case "call_expression":
				callText := gc.Text(source)
				if strings.Contains(callText, "createContext") {
					isContext = true
				}
			}
		}

		if isContext && name != "" {
			*contexts = append(*contexts, Context{
				Name:     name,
				FilePath: filePath,
				Line:     int(node.StartPoint().Row) + 1,
			})
		}
	}
}

// --- React Native: StyleSheet ---

func (t *TreeSitterAnalyzer) detectStyleSheets(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []NativeStyle {
	var styles []NativeStyle

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Type(lang) != "lexical_declaration" && child.Type(lang) != "variable_declaration" {
			continue
		}

		for j := 0; j < child.ChildCount(); j++ {
			decl := child.Child(j)
			if decl.Type(lang) != "variable_declarator" {
				continue
			}

			var name string
			var callNode *gotreesitter.Node

			for k := 0; k < decl.ChildCount(); k++ {
				gc := decl.Child(k)
				switch gc.Type(lang) {
				case "identifier":
					name = gc.Text(source)
				case "call_expression":
					callText := gc.Text(source)
					if strings.Contains(callText, "StyleSheet.create") {
						callNode = gc
					}
				}
			}

			if callNode == nil || name == "" {
				continue
			}

			// Extract top-level style keys only from the argument object
			var keys []string
			// Navigate: call_expression → arguments → object
			for m := 0; m < callNode.ChildCount(); m++ {
				args := callNode.Child(m)
				if args.Type(lang) != "arguments" {
					continue
				}
				for n := 0; n < args.ChildCount(); n++ {
					obj := args.Child(n)
					if obj.Type(lang) != "object" {
						continue
					}
					// Only top-level pairs
					for o := 0; o < obj.ChildCount(); o++ {
						pair := obj.Child(o)
						if pair.Type(lang) == "pair" {
							key := pair.Child(0)
							if key != nil && key.Type(lang) == "property_identifier" {
								keys = append(keys, key.Text(source))
							}
						}
					}
				}
			}

			styles = append(styles, NativeStyle{
				Name:     name,
				Keys:     keys,
				FilePath: filePath,
				Line:     int(child.StartPoint().Row) + 1,
			})
		}
	}

	return styles
}

// --- React Native: Screen registrations ---

func (t *TreeSitterAnalyzer) detectScreens(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Screen {
	var screens []Screen

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		nodeType := node.Type(lang)
		if nodeType != "jsx_self_closing_element" && nodeType != "jsx_opening_element" {
			return
		}

		// Look for <X.Screen name="..." />
		text := node.Text(source)
		if !strings.Contains(text, ".Screen") {
			return
		}

		// Extract navigator prefix and screen name from attributes
		var navigator, screenName, component string

		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			childType := child.Type(lang)

			if childType == "member_expression" || childType == "jsx_namespace_name" {
				parts := child.Text(source)
				if idx := strings.Index(parts, "."); idx != -1 {
					navigator = parts[:idx]
				}
			}

			if childType == "jsx_attribute" {
				attrText := child.Text(source)
				if strings.HasPrefix(attrText, "name=") {
					// Extract value between quotes
					if start := strings.IndexAny(attrText, "'\""); start != -1 {
						end := strings.LastIndexAny(attrText, "'\"")
						if end > start {
							screenName = attrText[start+1 : end]
						}
					}
				}
				if strings.HasPrefix(attrText, "component=") {
					// Extract {ComponentName}
					if start := strings.Index(attrText, "{"); start != -1 {
						end := strings.Index(attrText, "}")
						if end > start {
							component = attrText[start+1 : end]
						}
					}
				}
			}
		}

		if screenName != "" {
			screens = append(screens, Screen{
				Name:       screenName,
				ScreenName: screenName,
				Component:  component,
				Navigator:  navigator,
				FilePath:   filePath,
				Line:       int(node.StartPoint().Row) + 1,
			})
		}
	})

	return screens
}

// --- React Native: Navigators ---

func (t *TreeSitterAnalyzer) detectNavigators(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []NavigationRef {
	var navs []NavigationRef

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(lang)
		if nodeType != "lexical_declaration" && nodeType != "variable_declaration" {
			continue
		}

		for j := 0; j < child.ChildCount(); j++ {
			decl := child.Child(j)
			if decl.Type(lang) != "variable_declarator" {
				continue
			}

			var name string
			var callText string

			for k := 0; k < decl.ChildCount(); k++ {
				gc := decl.Child(k)
				switch gc.Type(lang) {
				case "identifier":
					name = gc.Text(source)
				case "call_expression":
					callText = gc.Text(source)
				}
			}

			if name == "" || !strings.Contains(callText, "Navigator") {
				continue
			}

			// Extract navigator type from createXxxNavigator
			navType := ""
			if idx := strings.Index(callText, "create"); idx != -1 {
				rest := callText[idx+len("create"):]
				if end := strings.Index(rest, "Navigator"); end != -1 {
					navType = rest[:end]
				}
			}

			if navType != "" {
				navs = append(navs, NavigationRef{
					Type:     navType,
					Name:     name,
					FilePath: filePath,
					Line:     int(child.StartPoint().Row) + 1,
				})
			}
		}
	}

	return navs
}

// --- React Native: Native modules ---

func (t *TreeSitterAnalyzer) detectNativeModules(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []NativeModule {
	var modules []NativeModule
	seen := make(map[string]bool)

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "identifier" {
			return
		}
		name := node.Text(source)
		category, isModule := rnModules[name]
		if !isModule || seen[name] {
			return
		}
		seen[name] = true

		modules = append(modules, NativeModule{
			Module:   name,
			Category: category,
			FilePath: filePath,
			Line:     int(node.StartPoint().Row) + 1,
		})
	})

	return modules
}

// --- Helpers ---

func (t *TreeSitterAnalyzer) extractDestructuredProps(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []string {
	var props []string

	t.walkTree(node, lang, func(n *gotreesitter.Node) {
		nt := n.Type(lang)
		if nt == "shorthand_property_identifier_pattern" || (nt == "identifier" && n.Parent() != nil &&
			n.Parent().Type(lang) == "object_pattern") {
			props = append(props, n.Text(source))
		}
	})

	return props
}

// walkTree recursively visits all nodes
func (t *TreeSitterAnalyzer) walkTree(node *gotreesitter.Node, lang *gotreesitter.Language, fn func(*gotreesitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := 0; i < node.ChildCount(); i++ {
		t.walkTree(node.Child(i), lang, fn)
	}
}

// findNodeRecursive checks if any descendant matches the predicate
func (t *TreeSitterAnalyzer) findNodeRecursive(node *gotreesitter.Node, lang *gotreesitter.Language, predicate func(*gotreesitter.Node) bool) bool {
	if node == nil {
		return false
	}
	if predicate(node) {
		return true
	}
	for i := 0; i < node.ChildCount(); i++ {
		if t.findNodeRecursive(node.Child(i), lang, predicate) {
			return true
		}
	}
	return false
}
