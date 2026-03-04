package react

import (
	"testing"
)

func TestAnalyzer_FunctionalComponents(t *testing.T) {
	source := `
import React from 'react';

export const UserProfile = ({ name, age, email }) => {
    const [loading, setLoading] = useState(false);
    
    useEffect(() => {
        fetchUser();
    }, []);

    return <div className="profile">
        <h1>{name}</h1>
        <p>{age}</p>
    </div>;
};

export default function Dashboard({ user }) {
    const theme = useContext(ThemeContext);
    const [data, setData] = useState(null);

    return <main>
        <UserProfile name={user.name} />
    </main>;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")

	if len(info.Components) < 1 {
		t.Fatalf("expected at least 1 component, got %d", len(info.Components))
	}

	// Should detect hooks
	if len(info.Hooks) < 3 {
		t.Errorf("expected at least 3 hook calls, got %d", len(info.Hooks))
	}

	// Check that useState is detected as built-in
	foundBuiltin := false
	for _, h := range info.Hooks {
		if h.Name == "useState" && !h.IsCustom {
			foundBuiltin = true
		}
	}
	if !foundBuiltin {
		t.Error("expected useState as built-in hook")
	}
}

func TestAnalyzer_ClassComponent(t *testing.T) {
	source := `
import React, { Component } from 'react';

class TodoList extends Component {
    constructor(props) {
        super(props);
        this.state = { items: [] };
    }

    render() {
        return <ul>
            {this.state.items.map(item => <li>{item}</li>)}
        </ul>;
    }
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "test.jsx")

	if len(info.Components) != 1 {
		t.Fatalf("expected 1 class component, got %d", len(info.Components))
	}

	comp := info.Components[0]
	if comp.Name != "TodoList" {
		t.Errorf("expected 'TodoList', got '%s'", comp.Name)
	}
	if comp.Type != "class" {
		t.Errorf("expected type 'class', got '%s'", comp.Type)
	}
}

func TestAnalyzer_CreateContext(t *testing.T) {
	source := `
import React from 'react';

const ThemeContext = React.createContext('light');
const AuthContext = createContext(null);

export default function App() {
    return <ThemeContext.Provider value="dark">
        <Main />
    </ThemeContext.Provider>;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")

	if len(info.Contexts) != 2 {
		t.Fatalf("expected 2 contexts, got %d", len(info.Contexts))
	}

	if info.Contexts[0].Name != "ThemeContext" {
		t.Errorf("expected 'ThemeContext', got '%s'", info.Contexts[0].Name)
	}
	if info.Contexts[1].Name != "AuthContext" {
		t.Errorf("expected 'AuthContext', got '%s'", info.Contexts[1].Name)
	}
}

func TestIsReactFile(t *testing.T) {
	tests := []struct {
		source   string
		expected bool
	}{
		{`import React from 'react';`, true},
		{`const el = <div>hello</div>;`, false}, // lowercase, no match
		{`return <MyComponent />`, true},
		{`console.log("hello")`, false},
	}

	for _, tt := range tests {
		result := IsReactFile(tt.source)
		if result != tt.expected {
			t.Errorf("IsReactFile(%q...) = %v, want %v", tt.source[:20], result, tt.expected)
		}
	}
}

func TestAnalyzer_CustomHooks(t *testing.T) {
	source := `
import React from 'react';

export function useDebounce(value, delay) {
    const [debouncedValue, setDebouncedValue] = useState(value);

    useEffect(() => {
        const timer = setTimeout(() => setDebouncedValue(value), delay);
        return () => clearTimeout(timer);
    }, [value, delay]);

    return <div>{debouncedValue}</div>;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")

	// useDebounce is a custom hook, should detect its internal hooks
	foundCustom := false
	for _, h := range info.Hooks {
		if h.Name == "useDebounce" && h.IsCustom {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Error("expected useDebounce as custom hook")
	}
}

func TestAnalyzer_NonReactFile(t *testing.T) {
	source := `
const express = require('express');
const app = express();
app.get('/', (req, res) => res.send('hello'));
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "server.js")

	if len(info.Components) != 0 {
		t.Errorf("expected 0 components for non-React file, got %d", len(info.Components))
	}
}

func TestAnalyzer_ReactNativeComponent(t *testing.T) {
	source := `
import React from 'react';
import { View, Text, StyleSheet } from 'react-native';

export default function HomeScreen({ navigation }) {
    const [count, setCount] = useState(0);

    return <View style={styles.container}>
        <Text>Welcome</Text>
    </View>;
}

const styles = StyleSheet.create({
    container: {
        flex: 1,
        justifyContent: 'center',
    },
    title: {
        fontSize: 24,
        fontWeight: 'bold',
    },
});
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "screens/HomeScreen.tsx")

	if !info.IsReactNative {
		t.Error("expected IsReactNative=true")
	}

	if len(info.Components) < 1 {
		t.Fatalf("expected at least 1 component, got %d", len(info.Components))
	}

	comp := info.Components[0]
	if !comp.IsNative {
		t.Error("component should be IsNative=true")
	}
	if !comp.IsScreen {
		t.Error("HomeScreen should be detected as screen")
	}

	// StyleSheet detection
	if len(info.NativeStyles) != 1 {
		t.Fatalf("expected 1 stylesheet, got %d", len(info.NativeStyles))
	}
	if info.NativeStyles[0].Name != "styles" {
		t.Errorf("expected 'styles', got '%s'", info.NativeStyles[0].Name)
	}
	if len(info.NativeStyles[0].Keys) != 2 {
		t.Errorf("expected 2 style keys, got %d: %v", len(info.NativeStyles[0].Keys), info.NativeStyles[0].Keys)
	}
}

func TestAnalyzer_ReactNativeNavigation(t *testing.T) {
	source := `
import React from 'react';
import { View, Text } from 'react-native';
import { createStackNavigator } from '@react-navigation/stack';

const Stack = createStackNavigator();

export default function AppNavigator() {
    return <Stack.Navigator>
        <Stack.Screen name="Home" component={HomeScreen} />
        <Stack.Screen name="Profile" component={ProfileScreen} />
        <Stack.Screen name="Settings" />
    </Stack.Navigator>;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "navigation/AppNavigator.tsx")

	if !info.IsReactNative {
		t.Error("expected IsReactNative=true")
	}

	// Navigator detection
	if len(info.Navigation) != 1 {
		t.Fatalf("expected 1 navigator, got %d", len(info.Navigation))
	}
	if info.Navigation[0].Type != "Stack" {
		t.Errorf("expected type 'Stack', got '%s'", info.Navigation[0].Type)
	}
	if info.Navigation[0].Name != "Stack" {
		t.Errorf("expected name 'Stack', got '%s'", info.Navigation[0].Name)
	}

	// Screen registrations
	if len(info.Screens) < 2 {
		t.Fatalf("expected at least 2 screens, got %d", len(info.Screens))
	}
	if info.Screens[0].ScreenName != "Home" {
		t.Errorf("expected screen 'Home', got '%s'", info.Screens[0].ScreenName)
	}
	if info.Screens[0].Component != "HomeScreen" {
		t.Errorf("expected component 'HomeScreen', got '%s'", info.Screens[0].Component)
	}
}

func TestAnalyzer_RNHooks(t *testing.T) {
	source := `
import React from 'react';
import { View } from 'react-native';
import { useNavigation, useRoute } from '@react-navigation/native';

export function ProfileScreen() {
    const navigation = useNavigation();
    const route = useRoute();
    const [data, setData] = useState(null);

    return <View />;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "ProfileScreen.tsx")

	// Check RN hooks are classified correctly
	foundRNHook := false
	foundBuiltin := false
	for _, h := range info.Hooks {
		if h.Name == "useNavigation" && h.IsRN && !h.IsCustom {
			foundRNHook = true
		}
		if h.Name == "useState" && !h.IsCustom && !h.IsRN {
			foundBuiltin = true
		}
	}
	if !foundRNHook {
		t.Error("expected useNavigation as RN hook")
	}
	if !foundBuiltin {
		t.Error("expected useState as built-in React hook")
	}
}

func TestAnalyzer_PlatformSpecific(t *testing.T) {
	source := `
import React from 'react';
import { View, Text, Platform } from 'react-native';

export function IOSButton() {
    return <View />;
}
`
	analyzer := NewAnalyzer()

	// iOS specific file
	info := analyzer.Analyze(source, "components/Button.ios.tsx")
	if len(info.Components) > 0 && info.Components[0].Platform != "ios" {
		t.Errorf("expected platform 'ios', got '%s'", info.Components[0].Platform)
	}

	// Android specific file
	info = analyzer.Analyze(source, "components/Button.android.tsx")
	if len(info.Components) > 0 && info.Components[0].Platform != "android" {
		t.Errorf("expected platform 'android', got '%s'", info.Components[0].Platform)
	}
}

func TestAnalyzer_NativeModules(t *testing.T) {
	source := `
import React from 'react';
import { View, Platform, Linking, AppState } from 'react-native';
import AsyncStorage from '@react-native-async-storage/async-storage';

export function SettingsScreen() {
    const handleLink = () => Linking.openURL('https://example.com');
    const os = Platform.OS;
    
    return <View />;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "SettingsScreen.tsx")

	if len(info.NativeModules) < 2 {
		t.Fatalf("expected at least 2 native modules, got %d", len(info.NativeModules))
	}

	foundPlatform := false
	foundLinking := false
	for _, m := range info.NativeModules {
		if m.Module == "Platform" && m.Category == "platform" {
			foundPlatform = true
		}
		if m.Module == "Linking" && m.Category == "linking" {
			foundLinking = true
		}
	}
	if !foundPlatform {
		t.Error("expected Platform module")
	}
	if !foundLinking {
		t.Error("expected Linking module")
	}
}

func TestIsReactNativeFile(t *testing.T) {
	tests := []struct {
		source   string
		expected bool
	}{
		{`import { View } from 'react-native';`, true},
		{`import { useNavigation } from '@react-navigation/native';`, true},
		{`import React from 'react';`, false},
		{`console.log("hello")`, false},
	}

	for i, tt := range tests {
		result := IsReactNativeFile(tt.source)
		if result != tt.expected {
			t.Errorf("test %d: IsReactNativeFile = %v, want %v", i, result, tt.expected)
		}
	}
}
