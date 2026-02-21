"""LLDB script to fetch SwiftUI view debug data from a _UIHostingView.

Uses _viewDebugData() with Mirror reflection to extract the SwiftUI view
tree as JSON, including text content and modifier values.
"""

from __future__ import annotations

import json

import lldb

# Swift function that recursively converts _ViewDebug.Data to NSDictionary.
# Accesses internal `data` (Dictionary<Property, Any>) and `childData` (Array<Data>)
# via Mirror to bypass access control.
_EXTRACT_NODE_SWIFT = """
func extractNode(_ node: _ViewDebug.Data) -> NSDictionary {
    var result = [String: Any]()
    let m = Mirror(reflecting: node)
    for child in m.children {
        switch child.label {
        case "data":
            for entry in Mirror(reflecting: child.value).children {
                let kv = Array(Mirror(reflecting: entry.value).children)
                guard kv.count == 2 else { continue }
                result["\\(kv[0].value)"] = "\\(kv[1].value)" as NSString
            }
        case "childData":
            if let arr = child.value as? [_ViewDebug.Data] {
                result["children"] = arr.map { extractNode($0) } as NSArray
            }
        default: break
        }
    }
    return result as NSDictionary
}
"""


def _run_swift_expression(debugger, expr):
    """Run a multi-line Swift expression via SBFrame.EvaluateExpression.

    Uses SetIgnoreBreakpoints(True) to avoid EXC_BREAKPOINT failures
    when ObjC expressions have been evaluated earlier in the same
    LLDB session (e.g. by fetch_hierarchy.py).
    """
    frame = (
        debugger.GetSelectedTarget()
        .GetProcess()
        .GetSelectedThread()
        .GetSelectedFrame()
    )
    opts = lldb.SBExpressionOptions()
    opts.SetLanguage(lldb.eLanguageTypeSwift)
    opts.SetTimeoutInMicroSeconds(60_000_000)
    opts.SetIgnoreBreakpoints(True)
    val = frame.EvaluateExpression(expr, opts)
    if val.GetError().Success():
        return val.GetObjectDescription() or val.GetValue()
    return None


def fetch_swiftui_tree_command(debugger, command, result, internal_dict):
    """LLDB command: fetch_swiftui_tree <address> [output_path]

    Calls _viewDebugData() on the given _UIHostingView address via Mirror
    and writes the resulting JSON to the given file path.
    """
    args = command.strip().split()
    if len(args) < 2:
        result.SetError("Usage: fetch_swiftui_tree <address> <output_path>")
        return

    address = args[0]
    output_path = args[1]

    # Check that SWIFTUI_VIEW_DEBUG=287 is set in the target process.
    # Use Swift (not ObjC) to avoid language-switch issues that cause
    # EXC_BREAKPOINT in the subsequent Swift expression evaluation.
    env_val = _run_swift_expression(
        debugger,
        """
        import Foundation
        ProcessInfo.processInfo.environment["SWIFTUI_VIEW_DEBUG"] ?? "NOT_SET"
        """,
    )
    if env_val is None or "287" not in env_val:
        result.SetError(
            "SWIFTUI_VIEW_DEBUG_NOT_SET: "
            "The target process was not launched with SWIFTUI_VIEW_DEBUG=287. "
            "_viewDebugData() requires this environment variable."
        )
        return

    # Convert address string to integer for unsafeBitCast
    expr = f"""
    import SwiftUI
    import Foundation
    {_EXTRACT_NODE_SWIFT}
    let raw = unsafeBitCast({address} as Int, to: _UIHostingView<AnyView>.self)._viewDebugData()
    let result = raw.map {{ extractNode($0) }} as NSArray
    let json = try! JSONSerialization.data(withJSONObject: result)
    String(data: json, encoding: .utf8)!
    """

    output = _run_swift_expression(debugger, expr)
    if not output:
        result.SetError(
            f"Failed to call _viewDebugData() on view at {address}"
        )
        return

    # Validate JSON and write to file
    try:
        parsed = json.loads(output)
        with open(output_path, "w") as f:
            json.dump(parsed, f, ensure_ascii=False)
    except (json.JSONDecodeError, OSError) as e:
        result.SetError(f"Failed to write SwiftUI tree JSON: {e}")
        return

    result.AppendMessage(f"SwiftUI tree saved to {output_path}")


def __lldb_init_module(debugger, internal_dict):
    debugger.HandleCommand(
        "command script add -f fetch_swiftui_tree.fetch_swiftui_tree_command fetch_swiftui_tree"
    )
