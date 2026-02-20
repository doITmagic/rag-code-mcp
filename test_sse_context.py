import urllib.request
import json
import threading
import sys
import os

def run_client():
    url = "http://localhost:3000/sse"
    try:
        req = urllib.request.Request(url)
        resp = urllib.request.urlopen(req)
    except Exception as e:
        print(f"Failed to connect to SSE: {e}")
        return
        
    print("Connected to SSE.")
    
    def do_post(path, data_obj):
        post_url = path if path.startswith("http") else f"http://localhost:3000{path}"
        req = urllib.request.Request(post_url, data=json.dumps(data_obj).encode(), headers={"Content-Type": "application/json"})
        urllib.request.urlopen(req)
        
    for raw_line in resp:
        line = raw_line.decode('utf-8').strip()
        if line.startswith("event: endpoint"):
            dataline = next(resp).decode('utf-8').strip()
            endpoint = dataline.split("data: ")[1]
            print(f"Got endpoint: {endpoint}")
            
            def init_and_call():
                try:
                    init_payload = {
                        "jsonrpc": "2.0",
                        "id": 1,
                        "method": "initialize",
                        "params": {
                            "protocolVersion": "2024-11-05", 
                            "capabilities": {},
                            "clientInfo": {"name": "testClient", "version": "1.0.0"}
                        }
                    }
                    do_post(endpoint, init_payload)
                    
                    notif_payload = {
                        "jsonrpc": "2.0",
                        "method": "notifications/initialized"
                    }
                    do_post(endpoint, notif_payload)
                    
                    tool_payload = {
                        "jsonrpc": "2.0",
                        "id": 2,
                        "method": "tools/call",
                        "params": {
                            "name": "rag_read_file_context",
                            "arguments": {
                                "file_path": "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/internal/service/tools/response.go",
                                "line_number": 15
                            }
                        }
                    }
                    print("Sending tool call for response.go at line 15...")
                    do_post(endpoint, tool_payload)
                except Exception as e:
                    print(f"Error in init_and_call: {e}")
                    
            t = threading.Thread(target=init_and_call)
            t.daemon = True
            t.start()
            
        elif line.startswith("data: "):
            try:
                data = json.loads(line[6:])
                if data.get("id") == 1:
                    print("Initialized.")
                elif data.get("id") == 2:
                    print(f"\nTool call result:\n{json.dumps(data, indent=2)}")
                    print("\nTest Complete! Exiting.")
                    os._exit(0)
            except json.JSONDecodeError:
                pass

if __name__ == "__main__":
    t = threading.Thread(target=run_client)
    t.start()
    t.join(timeout=10)
    print("Timeout or finished.")
