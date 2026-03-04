package react

// ReactInfo contains React and React Native analysis results
type ReactInfo struct {
	Components    []Component     `json:"components,omitempty"`
	Hooks         []HookUsage     `json:"hooks,omitempty"`
	Contexts      []Context       `json:"contexts,omitempty"`
	Screens       []Screen        `json:"screens,omitempty"`        // RN screens
	NativeStyles  []NativeStyle   `json:"native_styles,omitempty"`  // StyleSheet.create
	NativeModules []NativeModule  `json:"native_modules,omitempty"` // NativeModules usage
	Navigation    []NavigationRef `json:"navigation,omitempty"`     // Navigation config
	IsReactNative bool            `json:"is_react_native"`
}

// Component represents a React/RN component (functional or class)
type Component struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"` // "functional" or "class"
	Props      []string `json:"props,omitempty"`
	Hooks      []string `json:"hooks,omitempty"` // Hooks used in this component
	IsExported bool     `json:"is_exported,omitempty"`
	IsDefault  bool     `json:"is_default,omitempty"`
	HasJSX     bool     `json:"has_jsx"`
	IsScreen   bool     `json:"is_screen,omitempty"` // RN: navigation screen
	IsNative   bool     `json:"is_native,omitempty"` // Uses RN components
	Platform   string   `json:"platform,omitempty"`  // ios, android, or empty for both
	FilePath   string   `json:"file_path"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
}

// HookUsage represents a React hook call
type HookUsage struct {
	Name     string `json:"name"` // useState, useEffect, useNavigation, useCustom
	IsCustom bool   `json:"is_custom"`
	IsRN     bool   `json:"is_rn,omitempty"` // React Navigation hook
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// Context represents a React context creation
type Context struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// Screen represents a React Native navigation screen
type Screen struct {
	Name       string `json:"name"`
	ScreenName string `json:"screen_name,omitempty"` // <Stack.Screen name="Home" />
	Component  string `json:"component,omitempty"`
	Navigator  string `json:"navigator,omitempty"` // Stack, Tab, Drawer
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
}

// NativeStyle represents a StyleSheet.create() call
type NativeStyle struct {
	Name     string   `json:"name"`           // Variable name (e.g., "styles")
	Keys     []string `json:"keys,omitempty"` // Style keys defined (e.g., container, header)
	FilePath string   `json:"file_path"`
	Line     int      `json:"line"`
}

// NativeModule represents React Native module usage
type NativeModule struct {
	Module   string `json:"module"`   // e.g., AsyncStorage, Linking, Platform
	Category string `json:"category"` // storage, linking, platform, camera, etc.
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// NavigationRef represents a navigation configuration
type NavigationRef struct {
	Type     string `json:"type"` // Stack, Tab, Drawer, Native
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}
