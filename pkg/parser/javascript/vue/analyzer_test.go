package vue

import (
	"testing"
)

func TestAnalyzer_SFCOptionsAPI(t *testing.T) {
	source := `
<template>
    <div>
        <h1>{{ title }}</h1>
        <button @click="increment">Count: {{ count }}</button>
    </div>
</template>

<script>
export default {
    name: 'Counter',
    props: {
        initialCount: {
            type: Number,
            required: true
        }
    },
    emits: ['updated'],
    data() {
        return { count: this.initialCount };
    },
    methods: {
        increment() {
            this.count++;
            this.$emit('updated', this.count);
        }
    },
    mounted() {
        console.log('mounted');
    }
};
</script>

<style scoped>
h1 { color: blue; }
</style>
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "Counter.vue")

	if !info.IsSFC {
		t.Error("expected IsSFC=true")
	}

	if len(info.Components) < 1 {
		t.Fatal("expected at least 1 component")
	}

	comp := info.Components[0]
	if comp.Name != "Counter" {
		t.Errorf("expected name 'Counter', got '%s'", comp.Name)
	}
	if !comp.HasTemplate {
		t.Error("expected HasTemplate=true")
	}
	if comp.Type != "options" {
		t.Errorf("expected type 'options', got '%s'", comp.Type)
	}
}

func TestAnalyzer_SFCScriptSetup(t *testing.T) {
	source := `
<template>
    <div>{{ count }}</div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';

const props = defineProps(['initialCount']);
const emit = defineEmits(['updated']);

const count = ref(0);
const doubled = computed(() => count.value * 2);

onMounted(() => {
    count.value = props.initialCount;
});
</script>
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "Counter.vue")

	if !info.IsSFC {
		t.Error("expected IsSFC=true")
	}

	if len(info.Components) < 1 {
		t.Fatal("expected at least 1 component")
	}

	comp := info.Components[0]
	if comp.Type != "script-setup" {
		t.Errorf("expected 'script-setup', got '%s'", comp.Type)
	}
}

func TestAnalyzer_CompositionAPI(t *testing.T) {
	source := `
import { ref, reactive, computed, watch, onMounted } from 'vue';

export default defineComponent({
    setup() {
        const count = ref(0);
        const state = reactive({ name: 'Vue' });
        const doubled = computed(() => count.value * 2);
        watch(count, (val) => console.log(val));
        onMounted(() => console.log('ready'));
        return { count, state, doubled };
    }
});
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app.ts")

	if len(info.Composables) < 1 {
		t.Fatalf("expected composables, got %d", len(info.Composables))
	}

	names := make(map[string]bool)
	for _, c := range info.Composables {
		names[c.Name] = true
	}

	for _, expected := range []string{"ref", "reactive", "computed"} {
		if !names[expected] {
			t.Errorf("expected composable '%s'", expected)
		}
	}
}

func TestAnalyzer_CustomComposable(t *testing.T) {
	source := `
import { ref } from 'vue';

const count = ref(0);
const data = useCustomHook();
const auth = useAuth();
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app.ts")

	foundCustom := false
	for _, c := range info.Composables {
		if c.Name == "useCustomHook" && c.IsCustom {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Error("expected useCustomHook as custom composable")
	}
}

func TestAnalyzer_PiniaStore(t *testing.T) {
	source := `
import { defineStore } from 'pinia';

export const useCounterStore = defineStore('counter', {
    state: () => ({
        count: 0,
        name: 'Eduardo'
    }),
    getters: {
        doubleCount(state) {
            return state.count * 2;
        }
    },
    actions: {
        increment() {
            this.count++;
        }
    }
});
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "stores/counter.ts")

	if info.Store == nil {
		t.Fatal("expected store detection")
	}
	if info.Store.Type != "pinia" {
		t.Errorf("expected 'pinia', got '%s'", info.Store.Type)
	}
	if info.Store.Name != "counter" {
		t.Errorf("expected store name 'counter', got '%s'", info.Store.Name)
	}
}

func TestAnalyzer_VuexStore(t *testing.T) {
	source := `
import Vuex from 'vuex';

export default new Vuex.Store({
    state: {
        count: 0
    },
    mutations: {
        increment(state) { state.count++; }
    },
    actions: {
        asyncIncrement({ commit }) { commit('increment'); }
    },
    getters: {
        doubleCount(state) { return state.count * 2; }
    }
});
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "store.js")

	if info.Store == nil {
		t.Fatal("expected store detection")
	}
	if info.Store.Type != "vuex" {
		t.Errorf("expected 'vuex', got '%s'", info.Store.Type)
	}
}

func TestAnalyzer_Plugins(t *testing.T) {
	source := `
import { createApp } from 'vue';
import router from './router';
import pinia from './pinia';

const app = createApp(App);
app.use(router);
app.use(pinia);
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "main.ts")

	if len(info.Plugins) < 2 {
		t.Fatalf("expected at least 2 plugins, got %d", len(info.Plugins))
	}
}

func TestAnalyzer_Directives(t *testing.T) {
	source := `
import { createApp } from 'vue';

const app = createApp(App);
app.directive('focus', {
    mounted(el) { el.focus(); }
});
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "main.ts")

	if len(info.Directives) < 1 {
		t.Fatalf("expected at least 1 directive, got %d", len(info.Directives))
	}
	if info.Directives[0].Name != "v-focus" {
		t.Errorf("expected 'v-focus', got '%s'", info.Directives[0].Name)
	}
}

func TestIsVueFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"App.vue", true},
		{"components/Header.vue", true},
		{"app.ts", false},
		{"app.js", false},
	}

	for _, tt := range tests {
		result := IsVueFile(tt.path)
		if result != tt.expected {
			t.Errorf("IsVueFile(%q) = %v, want %v", tt.path, result, tt.expected)
		}
	}
}

func TestIsVueProject(t *testing.T) {
	tests := []struct {
		source   string
		expected bool
	}{
		{`import { ref } from 'vue';`, true},
		{`import { defineComponent } from 'vue';`, true},
		{`import React from 'react';`, false},
	}

	for _, tt := range tests {
		result := IsVueProject(tt.source)
		if result != tt.expected {
			t.Errorf("IsVueProject() = %v, want %v for: %s", result, tt.expected, tt.source[:30])
		}
	}
}
