package vue

// VueInfo holds Vue.js-specific analysis results
type VueInfo struct {
	Components  []Component  // Detected Vue components
	Composables []Composable // Composition API composables (useXxx)
	Store       *StoreInfo   // Vuex/Pinia store detection
	Directives  []Directive  // Custom directive registrations
	Plugins     []Plugin     // Vue plugin registrations
	IsVue3      bool         // Vue 3 (Composition API) detected
	IsSFC       bool         // Single File Component (.vue)
}

// Component represents a Vue component
type Component struct {
	Name        string
	Type        string   // "options", "composition", "script-setup", "class"
	Props       []Prop   // Component props
	Emits       []string // Emitted events
	Slots       []string // Named slots
	Hooks       []string // Lifecycle hooks used
	IsExported  bool
	IsDefault   bool
	HasTemplate bool
	FilePath    string
	StartLine   int
	EndLine     int
}

// Prop represents a component prop definition
type Prop struct {
	Name     string
	Type     string // String, Number, Boolean, Object, Array, Function
	Required bool
	Default  string
}

// Composable represents a Composition API composable function
type Composable struct {
	Name      string
	IsCustom  bool     // Custom vs built-in (ref, reactive, computed, etc.)
	Refs      []string // ref() and reactive() calls inside
	Computeds []string // computed() calls inside
	Watchers  int      // watch/watchEffect count
	FilePath  string
	Line      int
}

// StoreInfo represents Vuex or Pinia store detection
type StoreInfo struct {
	Type      string   // "vuex", "pinia"
	Name      string   // Store name (Pinia)
	State     []string // State properties
	Getters   []string // Getter names
	Actions   []string // Action names
	Mutations []string // Mutation names (Vuex only)
	FilePath  string
	Line      int
}

// Directive represents a custom Vue directive
type Directive struct {
	Name     string // v-focus, v-click-outside, etc.
	IsGlobal bool   // app.directive() vs local directive
	FilePath string
	Line     int
}

// Plugin represents a Vue plugin registration
type Plugin struct {
	Name     string
	FilePath string
	Line     int
}
