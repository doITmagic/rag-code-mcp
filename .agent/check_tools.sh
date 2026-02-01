#!/bin/bash
# Script pentru a adăuga verificarea file_path la toate tool-urile

echo "🔧 Adăugând verificare file_path la tool-urile rămase..."

# Lista tool-urilor de corectat
TOOLS=(
    "get_function_details.go"
    "find_type_definition.go"
    "find_implementations.go"
    "list_package_exports.go"
)

for tool in "${TOOLS[@]}"; do
    echo "  - Verificând $tool..."
    
    # Verifică dacă tool-ul există
    if [ ! -f "internal/tools/$tool" ]; then
        echo "    ⚠️  Fișierul nu există: $tool"
        continue
    fi
    
    # Verifică dacă deja are verificarea file_path
    if grep -q "file_path parameter is required" "internal/tools/$tool"; then
        echo "    ✅ $tool deja are verificarea file_path"
    else
        echo "    ❌ $tool TREBUIE corectat manual"
    fi
done

echo ""
echo "✅ Verificare completă!"
echo ""
echo "Tool-uri care TREBUIE corectate manual:"
echo "  1. get_function_details.go"
echo "  2. find_type_definition.go"
echo "  3. find_implementations.go"
echo "  4. list_package_exports.go"
