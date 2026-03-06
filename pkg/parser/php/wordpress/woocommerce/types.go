package woocommerce

// WooCommerceInfo contains WooCommerce-specific analysis results
type WooCommerceInfo struct {
	Hooks    []WCHook    `json:"hooks,omitempty"`
	APICalls []WCAPICall `json:"api_calls,omitempty"`
}

// WCHookArea categorizes WooCommerce hooks by functional area
type WCHookArea string

const (
	WCAreaCart     WCHookArea = "cart"
	WCAreaCheckout WCHookArea = "checkout"
	WCAreaProduct  WCHookArea = "product"
	WCAreaOrder    WCHookArea = "order"
	WCAreaAccount  WCHookArea = "account"
	WCAreaAdmin    WCHookArea = "admin"
	WCAreaShipping WCHookArea = "shipping"
	WCAreaPayment  WCHookArea = "payment"
	WCAreaEmail    WCHookArea = "email"
	WCAreaGeneral  WCHookArea = "general"
)

// WCHook represents a WooCommerce-specific hook with area classification
type WCHook struct {
	HookName  string     `json:"hook_name"` // Full hook name (e.g., "woocommerce_before_cart")
	Area      WCHookArea `json:"area"`      // Functional area (cart, checkout, product, etc.)
	HookType  string     `json:"hook_type"` // action, filter, action_trigger, filter_trigger
	Callback  string     `json:"callback,omitempty"`
	Priority  int        `json:"priority,omitempty"`
	FilePath  string     `json:"file_path"`
	StartLine int        `json:"start_line"`
	EndLine   int        `json:"end_line"`
}

// WCAPICall represents a WooCommerce API function call
type WCAPICall struct {
	Function  string `json:"function"` // wc_get_product, wc_get_order, etc.
	Category  string `json:"category"` // product, order, cart, customer, etc.
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}
