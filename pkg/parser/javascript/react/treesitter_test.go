package react

import (
	"testing"
)

func TestTreeSitter_FunctionalComponent(t *testing.T) {
	source := []byte(`
import React from 'react';

export default function App({ name, age }) {
    const [count, setCount] = React.useState(0);
    return <div>{name} - {count}</div>;
}
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.Components) < 1 {
		t.Fatal("expected at least 1 component")
	}
	comp := info.Components[0]
	if comp.Name != "App" {
		t.Errorf("expected 'App', got '%s'", comp.Name)
	}
	if !comp.IsExported || !comp.IsDefault {
		t.Error("App should be exported default")
	}
	if !comp.HasJSX {
		t.Error("App should have JSX")
	}
	if comp.Type != "functional" {
		t.Errorf("expected 'functional', got '%s'", comp.Type)
	}
}

func TestTreeSitter_ArrowComponent(t *testing.T) {
	source := []byte(`
import React, { useState } from 'react';

export const Counter = ({ initial }) => {
    const [count, setCount] = useState(initial);
    return <button onClick={() => setCount(count + 1)}>{count}</button>;
};
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.Components) < 1 {
		t.Fatal("expected at least 1 component")
	}
	if info.Components[0].Name != "Counter" {
		t.Errorf("expected 'Counter', got '%s'", info.Components[0].Name)
	}
	if !info.Components[0].IsExported {
		t.Error("Counter should be exported")
	}
}

func TestTreeSitter_ClassComponent(t *testing.T) {
	source := []byte(`
import React, { Component } from 'react';

export class UserList extends Component {
    render() {
        return <ul>{this.props.users.map(u => <li>{u.name}</li>)}</ul>;
    }
}
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.Components) < 1 {
		t.Fatal("expected at least 1 class component")
	}
	if info.Components[0].Type != "class" {
		t.Error("expected class component")
	}
}

func TestTreeSitter_HookDetection(t *testing.T) {
	source := []byte(`
import React, { useState, useEffect, useCallback } from 'react';

function App() {
    const [data, setData] = useState(null);
    useEffect(() => { fetch('/api').then(r => setData(r)); }, []);
    const handleClick = useCallback(() => {}, []);
    return <div>{data}</div>;
}
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	hookNames := make(map[string]bool)
	for _, h := range info.Hooks {
		hookNames[h.Name] = true
	}

	for _, expected := range []string{"useState", "useEffect", "useCallback"} {
		if !hookNames[expected] {
			t.Errorf("expected hook %s", expected)
		}
	}
}

func TestTreeSitter_ReactNativeStyles(t *testing.T) {
	source := []byte(`
import React from 'react';
import { View, Text, StyleSheet } from 'react-native';

function HomeScreen() {
    return <View style={styles.container}><Text style={styles.title}>Hello</Text></View>;
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
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "screen.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if !info.IsReactNative {
		t.Error("expected IsReactNative=true")
	}

	if len(info.NativeStyles) < 1 {
		t.Fatal("expected at least 1 native style")
	}

	// Only top-level keys (container, title), NOT nested (flex, justifyContent, etc.)
	style := info.NativeStyles[0]
	if len(style.Keys) != 2 {
		t.Errorf("expected 2 top-level style keys, got %d: %v", len(style.Keys), style.Keys)
	}
}

func TestTreeSitter_ContextDetection(t *testing.T) {
	source := []byte(`
import React, { createContext } from 'react';

const ThemeContext = createContext('light');

function App() {
    return <ThemeContext.Provider value="dark"><div>Hello</div></ThemeContext.Provider>;
}
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "test.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.Contexts) < 1 {
		t.Fatal("expected at least 1 context")
	}
	if info.Contexts[0].Name != "ThemeContext" {
		t.Errorf("expected 'ThemeContext', got '%s'", info.Contexts[0].Name)
	}
}

func TestTreeSitter_CustomHookDefinition(t *testing.T) {
	source := []byte(`
import React, { useState, useEffect } from 'react';

export function useDebounce(value, delay) {
    const [debouncedValue, setDebouncedValue] = useState(value);
    useEffect(() => {
        const timer = setTimeout(() => setDebouncedValue(value), delay);
        return () => clearTimeout(timer);
    }, [value, delay]);
    return <div>{debouncedValue}</div>;
}
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "hooks.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	foundCustom := false
	for _, h := range info.Hooks {
		if h.Name == "useDebounce" && h.IsCustom {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Error("expected useDebounce as custom hook definition")
	}
}

func TestTreeSitter_RNNavigation(t *testing.T) {
	source := []byte(`
import React from 'react';
import { createStackNavigator } from '@react-navigation/stack';

const Stack = createStackNavigator();

function AppNavigator() {
    return (
        <Stack.Navigator>
            <Stack.Screen name="Home" component={HomeScreen} />
            <Stack.Screen name="Profile" component={ProfileScreen} />
        </Stack.Navigator>
    );
}
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "navigation.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if !info.IsReactNative {
		t.Error("expected IsReactNative=true")
	}

	if len(info.Navigation) < 1 {
		t.Fatal("expected at least 1 navigator")
	}
	if info.Navigation[0].Type != "Stack" {
		t.Errorf("expected Stack navigator, got %s", info.Navigation[0].Type)
	}
}

func TestTreeSitter_NativeModules(t *testing.T) {
	source := []byte(`
import React from 'react';
import { Platform, Linking, View } from 'react-native';

function App() {
    const os = Platform.OS;
    const openURL = () => Linking.openURL('https://example.com');
    return <View />;
}
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "app.tsx")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	moduleNames := make(map[string]bool)
	for _, m := range info.NativeModules {
		moduleNames[m.Module] = true
	}

	if !moduleNames["Platform"] {
		t.Error("expected Platform module")
	}
	if !moduleNames["Linking"] {
		t.Error("expected Linking module")
	}
}
