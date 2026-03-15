package vue

import (
	"regexp"
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// TreeSitterAnalyzer uses tree-sitter AST for Vue.js script content parsing.
// Caches Parser instances per language to avoid re-allocating expensive lookup tables.
type TreeSitterAnalyzer struct {
	mu      sync.Mutex
	parsers map[string]*gotreesitter.Parser
}

// NewTreeSitterAnalyzer creates a new tree-sitter based Vue analyzer
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

// AnalyzeScript parses the <script> content with tree-sitter
func (t *TreeSitterAnalyzer) AnalyzeScript(scriptContent []byte, filePath string, isSetup bool) *VueInfo {
	// Detect language from script tag (TS or JS)
	langFile := "script.ts"
	if !strings.HasSuffix(filePath, ".ts") && !strings.HasSuffix(filePath, ".tsx") {
		langFile = "script.tsx" // TSX for JSX support in Vue
	}

	lang := grammars.DetectLanguage(langFile)
	if lang == nil {
		return nil
	}

	parser := t.getOrCreateParser(lang)
	tree, err := parser.Parse(scriptContent)
	if err != nil {
		return nil
	}
	defer tree.Release()

	root := tree.RootNode()
	langObj := lang.Language()

	info := &VueInfo{}

	// Detect Vue 3 imports
	info.IsVue3 = t.hasVue3Import(root, scriptContent, langObj)

	// Detect composables from AST
	info.Composables = t.detectComposables(root, scriptContent, langObj, filePath)

	// Detect component from AST
	comp := t.detectComponent(root, scriptContent, langObj, filePath, isSetup)
	if comp != nil {
		info.Components = append(info.Components, *comp)
	}

	// Detect store usage
	info.Store = t.detectStore(root, scriptContent, langObj, filePath)

	// Detect plugins
	info.Plugins = t.detectPlugins(root, scriptContent, langObj, filePath)

	// Detect directives
	info.Directives = t.detectDirectives(root, scriptContent, langObj, filePath)

	return info
}

// hasVue3Import checks for Vue 3 imports
func (t *TreeSitterAnalyzer) hasVue3Import(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language) bool {
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Type(lang) == "import_statement" {
			text := child.Text(source)
			if strings.Contains(text, "'vue'") || strings.Contains(text, "\"vue\"") {
				return true
			}
		}
	}
	return false
}

// detectComposables finds Composition API calls via AST
func (t *TreeSitterAnalyzer) detectComposables(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Composable {
	var composables []Composable

	builtinVue := map[string]bool{
		"ref": true, "reactive": true, "computed": true,
		"watch": true, "watchEffect": true, "toRef": true,
		"toRefs": true, "shallowRef": true, "shallowReactive": true,
		"readonly": true, "provide": true, "inject": true,
		"nextTick": true, "useSlots": true, "useAttrs": true,
		"defineProps": true, "defineEmits": true, "defineExpose": true,
		"defineOptions": true, "withDefaults": true,
	}

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "call_expression" {
			return
		}

		callee := node.Child(0)
		if callee == nil {
			return
		}

		name := callee.Text(source)

		// ref, reactive, computed, watch, watchEffect
		if builtinVue[name] {
			composables = append(composables, Composable{
				Name:     name,
				FilePath: filePath,
				Line:     int(node.StartPoint().Row) + 1,
			})
			return
		}

		// Custom composables: useXxx
		if strings.HasPrefix(name, "use") && len(name) > 3 && name[3] >= 'A' && name[3] <= 'Z' {
			composables = append(composables, Composable{
				Name:     name,
				IsCustom: true,
				FilePath: filePath,
				Line:     int(node.StartPoint().Row) + 1,
			})
		}

		// Lifecycle hooks
		if compositionHooks[name] {
			composables = append(composables, Composable{
				Name:     name,
				FilePath: filePath,
				Line:     int(node.StartPoint().Row) + 1,
			})
		}
	})

	return composables
}

// detectComponent finds defineComponent or export default object
func (t *TreeSitterAnalyzer) detectComponent(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, isSetup bool) *Component {
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(lang)

		if nodeType == "export_statement" {
			text := child.Text(source)

			comp := &Component{
				FilePath:   filePath,
				IsExported: true,
				IsDefault:  true,
				StartLine:  int(child.StartPoint().Row) + 1,
				EndLine:    int(child.EndPoint().Row) + 1,
			}

			if isSetup {
				comp.Type = "script-setup"
			} else if strings.Contains(text, "defineComponent") {
				comp.Type = "composition"
			} else {
				comp.Type = "options"
			}

			// Extract component name from options
			if match := reComponentName.FindStringSubmatch(text); len(match) > 1 {
				comp.Name = match[1]
			}

			// Extract props from AST
			comp.Props = t.extractPropsFromAST(child, source, lang)

			return comp
		}
	}

	return nil
}

// extractPropsFromAST extracts props from defineProps or props object via AST
func (t *TreeSitterAnalyzer) extractPropsFromAST(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []Prop {
	var props []Prop

	t.walkTree(node, lang, func(n *gotreesitter.Node) {
		if n.Type(lang) != "call_expression" {
			return
		}

		callee := n.Child(0)
		if callee == nil || callee.Text(source) != "defineProps" {
			return
		}

		// Extract from arguments
		args := n.Child(1)
		if args == nil {
			return
		}

		for i := 0; i < args.ChildCount(); i++ {
			arg := args.Child(i)
			if arg.Type(lang) == "array" {
				// Array form: defineProps(['foo', 'bar'])
				for j := 0; j < arg.ChildCount(); j++ {
					el := arg.Child(j)
					if el.Type(lang) == "string" {
						name := strings.Trim(el.Text(source), "'\"")
						props = append(props, Prop{Name: name})
					}
				}
			}
		}
	})

	return props
}

// detectStore finds Vuex/Pinia store patterns from AST
func (t *TreeSitterAnalyzer) detectStore(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *StoreInfo {
	var store *StoreInfo

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		nodeType := node.Type(lang)

		// call_expression: defineStore(), createStore()
		if nodeType == "call_expression" {
			callee := node.Child(0)
			if callee == nil {
				return
			}

			name := callee.Text(source)

			if name == "defineStore" {
				store = &StoreInfo{
					Type:     "pinia",
					FilePath: filePath,
					Line:     int(node.StartPoint().Row) + 1,
				}
				args := node.Child(1)
				if args != nil {
					for i := 0; i < args.ChildCount(); i++ {
						arg := args.Child(i)
						if arg.Type(lang) == "string" {
							store.Name = strings.Trim(arg.Text(source), "'\"")
							break
						}
					}
				}
			}

			if name == "createStore" {
				store = &StoreInfo{
					Type:     "vuex",
					FilePath: filePath,
					Line:     int(node.StartPoint().Row) + 1,
				}
			}
		}

		// new_expression: new Vuex.Store({...})
		if nodeType == "new_expression" {
			text := node.Text(source)
			if strings.Contains(text, "Vuex.Store") {
				store = &StoreInfo{
					Type:     "vuex",
					FilePath: filePath,
					Line:     int(node.StartPoint().Row) + 1,
				}
			}
		}
	})

	return store
}

// detectPlugins finds app.use() calls from AST
func (t *TreeSitterAnalyzer) detectPlugins(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Plugin {
	var plugins []Plugin

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "call_expression" {
			return
		}

		callee := node.Child(0)
		if callee == nil || callee.Type(lang) != "member_expression" {
			return
		}

		if !strings.HasSuffix(callee.Text(source), ".use") {
			return
		}

		args := node.Child(1)
		if args == nil {
			return
		}

		for i := 0; i < args.ChildCount(); i++ {
			arg := args.Child(i)
			if arg.Type(lang) == "identifier" {
				plugins = append(plugins, Plugin{
					Name:     arg.Text(source),
					FilePath: filePath,
					Line:     int(node.StartPoint().Row) + 1,
				})
			}
		}
	})

	return plugins
}

// detectDirectives finds app.directive() calls from AST
func (t *TreeSitterAnalyzer) detectDirectives(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Directive {
	var directives []Directive

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "call_expression" {
			return
		}

		callee := node.Child(0)
		if callee == nil || callee.Type(lang) != "member_expression" {
			return
		}

		if !strings.HasSuffix(callee.Text(source), ".directive") {
			return
		}

		args := node.Child(1)
		if args == nil {
			return
		}

		for i := 0; i < args.ChildCount(); i++ {
			arg := args.Child(i)
			if arg.Type(lang) == "string" {
				name := strings.Trim(arg.Text(source), "'\"")
				directives = append(directives, Directive{
					Name:     "v-" + name,
					IsGlobal: true,
					FilePath: filePath,
					Line:     int(node.StartPoint().Row) + 1,
				})
				break
			}
		}
	})

	return directives
}

// extractScriptFromSFC extracts <script> content using regex
func extractScriptFromSFC(source string) (string, bool) {
	reScript := regexp.MustCompile(`(?s)<script(?:\s[^>]*)?>(.+?)</script>`)
	match := reScript.FindStringSubmatch(source)
	if len(match) < 2 {
		return "", false
	}

	isSetup := strings.Contains(source, "<script") && strings.Contains(source[:strings.Index(source, ">")+1], "setup")
	return match[1], isSetup
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
