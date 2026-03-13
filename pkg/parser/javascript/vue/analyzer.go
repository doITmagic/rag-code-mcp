package vue

import (
	"regexp"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	// Vue SFC block detection
	reTemplateBlock = regexp.MustCompile(`(?s)<template(?:\s[^>]*)?>(.+?)</template>`)
	reScriptSetup   = regexp.MustCompile(`<script\s+[^>]*setup[^>]*>`)

	// Options API
	reDefineComponent = regexp.MustCompile(`(?m)defineComponent\s*\(\s*\{`)
	reExportDefault   = regexp.MustCompile(`(?m)^export\s+default\s+\{`)
	reOptionsProps    = regexp.MustCompile(`(?m)^\s+props\s*:\s*[\[{]`)
	reOptionsEmits    = regexp.MustCompile(`(?m)^\s+emits\s*:\s*[\[{]`)
	reComponentName   = regexp.MustCompile(`(?m)^\s+name\s*:\s*['"](\w+)['"]`)

	// Composition API
	reRef         = regexp.MustCompile(`\bref\s*\(`)
	reReactive    = regexp.MustCompile(`\breactive\s*\(`)
	reComputed    = regexp.MustCompile(`\bcomputed\s*\(`)
	reWatch       = regexp.MustCompile(`\bwatch(?:Effect)?\s*\(`)
	reDefineProps = regexp.MustCompile(`(?m)defineProps\s*[<(]`)

	// Vue 3 imports
	reVue3Import = regexp.MustCompile(`(?m)^import\s+.*\s+from\s+['"]vue['"]`)

	// Props extraction
	rePropName  = regexp.MustCompile(`(?m)^\s+(\w+)\s*:\s*\{`)
	rePropType  = regexp.MustCompile(`type\s*:\s*(\w+)`)
	rePropReq   = regexp.MustCompile(`required\s*:\s*true`)
	reArrayProp = regexp.MustCompile(`['"](\w+)['"]`)

	// Lifecycle hooks (Options + Composition)
	optionsHooks = []string{
		"beforeCreate", "created", "beforeMount", "mounted",
		"beforeUpdate", "updated", "beforeUnmount", "unmounted",
		"beforeDestroy", "destroyed", "activated", "deactivated",
		"errorCaptured", "renderTracked", "renderTriggered",
	}
	compositionHooks = map[string]bool{
		"onBeforeMount": true, "onMounted": true,
		"onBeforeUpdate": true, "onUpdated": true,
		"onBeforeUnmount": true, "onUnmounted": true,
		"onActivated": true, "onDeactivated": true,
		"onErrorCaptured": true, "onRenderTracked": true,
		"onRenderTriggered": true,
	}

	// Vuex patterns
	reVuexStore = regexp.MustCompile(`(?m)(?:new\s+Vuex\.Store|createStore)\s*\(\s*\{`)

	// Pinia patterns
	rePiniaStore = regexp.MustCompile(`(?m)defineStore\s*\(\s*['"](\w+)['"]`)

	// Custom directives
	reDirectiveGlobal = regexp.MustCompile(`(?m)app\.directive\s*\(\s*['"](\w[\w-]*)['"]`)

	// Plugins
	rePluginUse = regexp.MustCompile(`(?m)app\.use\s*\(\s*(\w+)`)

	// Component methods/properties extraction
	reMethodName = regexp.MustCompile(`(?m)^\s+(\w+)\s*\(`)
)

// Analyzer detects Vue.js-specific patterns
type Analyzer struct {
	tsAnalyzer *TreeSitterAnalyzer
}

// NewAnalyzer creates a new Vue.js analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		tsAnalyzer: NewTreeSitterAnalyzer(),
	}
}

// IsVueFile checks if a file is a Vue SFC or contains Vue patterns
func IsVueFile(filePath string) bool {
	return strings.HasSuffix(filePath, ".vue")
}

// IsVueProject checks source for Vue imports
func IsVueProject(source string) bool {
	return reVue3Import.MatchString(source) ||
		strings.Contains(source, "from 'vue'") ||
		strings.Contains(source, "from \"vue\"") ||
		reDefineComponent.MatchString(source)
}

// Analyze performs Vue.js-specific analysis
// Uses tree-sitter for script parsing, regex for SFC block extraction
func (a *Analyzer) Analyze(source string, filePath string) *VueInfo {
	info := &VueInfo{}

	// SFC detection
	info.IsSFC = IsVueFile(filePath)

	// Extract script content from SFC
	scriptContent := source
	isSetup := false
	if info.IsSFC {
		if sc, setup := extractScriptFromSFC(source); sc != "" {
			scriptContent = sc
			isSetup = setup
		}
		info.Components = append(info.Components, a.detectSFCComponent(source, scriptContent, filePath))
	}

	// Try tree-sitter first for script content parsing
	tsInfo := a.tsAnalyzer.AnalyzeScript([]byte(scriptContent), filePath, isSetup)
	if tsInfo != nil && (len(tsInfo.Composables) > 0 || len(tsInfo.Components) > 0 || tsInfo.Store != nil) {
		// Merge tree-sitter results with SFC info
		info.IsVue3 = tsInfo.IsVue3
		info.Composables = tsInfo.Composables
		info.Store = tsInfo.Store
		info.Plugins = tsInfo.Plugins
		info.Directives = tsInfo.Directives
		if !info.IsSFC {
			info.Components = tsInfo.Components
		}
		return info
	}

	// Fallback to regex-based detection
	// Vue 3 detection
	info.IsVue3 = reVue3Import.MatchString(scriptContent) ||
		reDefineComponent.MatchString(scriptContent) ||
		reScriptSetup.MatchString(source) ||
		reDefineProps.MatchString(scriptContent)

	// Detect components in non-SFC files
	if !info.IsSFC {
		info.Components = a.detectComponents(scriptContent, filePath)
	}

	// Detect composables
	info.Composables = a.detectComposables(scriptContent, filePath)

	// Detect store patterns
	info.Store = a.detectStore(scriptContent, filePath)

	// Detect directives
	info.Directives = a.detectDirectives(scriptContent, filePath)

	// Detect plugins
	info.Plugins = a.detectPlugins(scriptContent, filePath)

	return info
}

// detectSFCComponent analyzes a Single File Component
func (a *Analyzer) detectSFCComponent(fullSource, scriptContent, filePath string) Component {
	comp := Component{
		FilePath:    filePath,
		HasTemplate: reTemplateBlock.MatchString(fullSource),
		IsExported:  true,
		IsDefault:   true,
		StartLine:   1,
	}

	// Determine component type
	if reScriptSetup.MatchString(fullSource) {
		comp.Type = "script-setup"
	} else if reDefineComponent.MatchString(scriptContent) {
		comp.Type = "composition"
	} else {
		comp.Type = "options"
	}

	// Extract component name
	if match := reComponentName.FindStringSubmatch(scriptContent); len(match) > 1 {
		comp.Name = match[1]
	} else {
		// Derive from filename
		name := strings.TrimSuffix(filePath, ".vue")
		if idx := strings.LastIndex(name, "/"); idx != -1 {
			name = name[idx+1:]
		}
		comp.Name = cases.Title(language.English).String(name)
	}

	// Extract props
	comp.Props = a.extractProps(scriptContent)

	// Extract emits
	comp.Emits = a.extractEmits(scriptContent)

	// Extract lifecycle hooks
	comp.Hooks = a.extractHooks(scriptContent)

	return comp
}

// detectComponents finds Vue component definitions in JS/TS files
func (a *Analyzer) detectComponents(source, filePath string) []Component {
	var components []Component

	if reDefineComponent.MatchString(source) || reExportDefault.MatchString(source) {
		comp := Component{
			FilePath:   filePath,
			IsExported: true,
			IsDefault:  true,
		}

		if reDefineComponent.MatchString(source) {
			comp.Type = "composition"
		} else {
			comp.Type = "options"
		}

		if match := reComponentName.FindStringSubmatch(source); len(match) > 1 {
			comp.Name = match[1]
		}

		comp.Props = a.extractProps(source)
		comp.Emits = a.extractEmits(source)
		comp.Hooks = a.extractHooks(source)

		components = append(components, comp)
	}

	return components
}

// extractProps extracts component props
func (a *Analyzer) extractProps(source string) []Prop {
	var props []Prop

	// script setup: defineProps<{...}>() or defineProps([...])
	if reDefineProps.MatchString(source) {
		// Simple array form: defineProps(['foo', 'bar'])
		if idx := strings.Index(source, "defineProps(["); idx != -1 {
			end := strings.Index(source[idx:], "])")
			if end != -1 {
				segment := source[idx : idx+end+2]
				for _, m := range reArrayProp.FindAllStringSubmatch(segment, -1) {
					props = append(props, Prop{Name: m[1]})
				}
			}
		}
		return props
	}

	// Options API: props: { name: { type: String, required: true } }
	if !reOptionsProps.MatchString(source) {
		return nil
	}

	// Find props block
	idx := reOptionsProps.FindStringIndex(source)
	if idx == nil {
		return nil
	}

	// Simple array form: props: ['foo', 'bar']
	afterProps := source[idx[0]:]
	if strings.Contains(afterProps[:min(50, len(afterProps))], "[") {
		end := strings.Index(afterProps, "]")
		if end != -1 {
			segment := afterProps[:end+1]
			for _, m := range reArrayProp.FindAllStringSubmatch(segment, -1) {
				props = append(props, Prop{Name: m[1]})
			}
		}
		return props
	}

	// Object form
	for _, m := range rePropName.FindAllStringSubmatch(afterProps, 10) {
		prop := Prop{Name: m[1]}
		// Look for type and required in the next few lines
		propIdx := strings.Index(afterProps, m[0])
		if propIdx != -1 {
			segment := afterProps[propIdx:min(propIdx+200, len(afterProps))]
			if tm := rePropType.FindStringSubmatch(segment); len(tm) > 1 {
				prop.Type = tm[1]
			}
			if rePropReq.MatchString(segment) {
				prop.Required = true
			}
		}
		props = append(props, prop)
	}

	return props
}

// extractEmits extracts component emitted events
func (a *Analyzer) extractEmits(source string) []string {
	var emits []string

	// script setup: defineEmits(['event1', 'event2'])
	if idx := strings.Index(source, "defineEmits(["); idx != -1 {
		end := strings.Index(source[idx:], "])")
		if end != -1 {
			segment := source[idx : idx+end+2]
			for _, m := range reArrayProp.FindAllStringSubmatch(segment, -1) {
				emits = append(emits, m[1])
			}
		}
		return emits
	}

	// Options API: emits: ['event1', 'event2']
	if idx := reOptionsEmits.FindStringIndex(source); idx != nil {
		afterEmits := source[idx[0]:]
		end := strings.Index(afterEmits, "]")
		if end != -1 {
			segment := afterEmits[:end+1]
			for _, m := range reArrayProp.FindAllStringSubmatch(segment, -1) {
				emits = append(emits, m[1])
			}
		}
	}

	return emits
}

// extractHooks extracts lifecycle hooks usage
func (a *Analyzer) extractHooks(source string) []string {
	var hooks []string
	seen := make(map[string]bool)

	// Options API hooks
	for _, hook := range optionsHooks {
		pattern := hook + "("
		if strings.Contains(source, pattern) || strings.Contains(source, hook+":") || strings.Contains(source, hook+" (") {
			if !seen[hook] {
				seen[hook] = true
				hooks = append(hooks, hook)
			}
		}
	}

	// Composition API hooks
	for hook := range compositionHooks {
		if strings.Contains(source, hook+"(") {
			if !seen[hook] {
				seen[hook] = true
				hooks = append(hooks, hook)
			}
		}
	}

	return hooks
}

// detectComposables finds Composition API composable functions
func (a *Analyzer) detectComposables(source, filePath string) []Composable {
	var composables []Composable

	// Built-in composables
	builtinComposables := map[string]bool{
		"ref": true, "reactive": true, "computed": true,
		"watch": true, "watchEffect": true, "toRef": true,
		"toRefs": true, "shallowRef": true, "shallowReactive": true,
		"readonly": true, "provide": true, "inject": true,
		"nextTick": true, "useSlots": true, "useAttrs": true,
	}

	// Detect ref() calls
	for _, m := range reRef.FindAllStringIndex(source, -1) {
		line := strings.Count(source[:m[0]], "\n") + 1
		composables = append(composables, Composable{
			Name:     "ref",
			FilePath: filePath,
			Line:     line,
		})
	}

	// Detect reactive() calls
	for _, m := range reReactive.FindAllStringIndex(source, -1) {
		line := strings.Count(source[:m[0]], "\n") + 1
		composables = append(composables, Composable{
			Name:     "reactive",
			FilePath: filePath,
			Line:     line,
		})
	}

	// Detect computed() calls
	for _, m := range reComputed.FindAllStringIndex(source, -1) {
		line := strings.Count(source[:m[0]], "\n") + 1
		composables = append(composables, Composable{
			Name:     "computed",
			FilePath: filePath,
			Line:     line,
		})
	}

	// Detect watch/watchEffect
	for _, m := range reWatch.FindAllStringIndex(source, -1) {
		line := strings.Count(source[:m[0]], "\n") + 1
		name := "watch"
		if strings.HasPrefix(source[m[0]:], "watchEffect") {
			name = "watchEffect"
		}
		composables = append(composables, Composable{
			Name:     name,
			FilePath: filePath,
			Line:     line,
		})
	}

	// Detect custom composables (useXxx)
	reCustomComposable := regexp.MustCompile(`\buse[A-Z]\w+\s*\(`)
	for _, m := range reCustomComposable.FindAllStringIndex(source, -1) {
		name := strings.TrimSuffix(source[m[0]:m[1]], "(")
		name = strings.TrimSpace(name)
		if !builtinComposables[name] && !seen(composables, name) {
			line := strings.Count(source[:m[0]], "\n") + 1
			composables = append(composables, Composable{
				Name:     name,
				IsCustom: true,
				FilePath: filePath,
				Line:     line,
			})
		}
	}

	return composables
}

// detectStore finds Vuex or Pinia store definitions
func (a *Analyzer) detectStore(source, filePath string) *StoreInfo {
	// Pinia
	if match := rePiniaStore.FindStringSubmatch(source); len(match) > 1 {
		store := &StoreInfo{
			Type:     "pinia",
			Name:     match[0],
			FilePath: filePath,
		}

		// Extract name
		store.Name = match[1]

		// Extract state, getters, actions from defineStore
		store.State = a.extractObjectKeys(source, "state")
		store.Getters = a.extractObjectKeys(source, "getters")
		store.Actions = a.extractObjectKeys(source, "actions")

		return store
	}

	// Vuex
	if reVuexStore.MatchString(source) {
		store := &StoreInfo{
			Type:     "vuex",
			FilePath: filePath,
		}

		store.State = a.extractObjectKeys(source, "state")
		store.Getters = a.extractObjectKeys(source, "getters")
		store.Mutations = a.extractObjectKeys(source, "mutations")
		store.Actions = a.extractObjectKeys(source, "actions")

		return store
	}

	return nil
}

// extractObjectKeys extracts property names from an object block
func (a *Analyzer) extractObjectKeys(source, blockName string) []string {
	var keys []string
	pattern := regexp.MustCompile(`(?m)^\s+` + blockName + `\s*:\s*(?:\(\s*\)\s*=>)?\s*\{`)
	idx := pattern.FindStringIndex(source)
	if idx == nil {
		return nil
	}

	// Find closing brace
	depth := 0
	start := idx[1] - 1
	for i := start; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				block := source[start+1 : i]
				for _, m := range reMethodName.FindAllStringSubmatch(block, -1) {
					keys = append(keys, m[1])
				}
				return keys
			}
		}
	}

	return keys
}

// detectDirectives finds custom Vue directive registrations
func (a *Analyzer) detectDirectives(source, filePath string) []Directive {
	var directives []Directive

	// Global directives: app.directive('focus', {...})
	for _, match := range reDirectiveGlobal.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1
		directives = append(directives, Directive{
			Name:     "v-" + name,
			IsGlobal: true,
			FilePath: filePath,
			Line:     line,
		})
	}

	return directives
}

// detectPlugins finds Vue plugin usage
func (a *Analyzer) detectPlugins(source, filePath string) []Plugin {
	var plugins []Plugin

	for _, match := range rePluginUse.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1
		plugins = append(plugins, Plugin{
			Name:     name,
			FilePath: filePath,
			Line:     line,
		})
	}

	return plugins
}

// helper to check if a composable name is already in the list
func seen(composables []Composable, name string) bool {
	for _, c := range composables {
		if c.Name == name {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
