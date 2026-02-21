"""LLDB script to fetch view hierarchy via DBGViewDebuggerSupport_iOS.

Loads libViewDebuggerSupport.dylib and calls fetchViewHierarchy,
saving the resulting bplist to a specified file path.
"""

from __future__ import annotations

import lldb


def _run_expression(debugger, expr, language="objc"):
    """Run a single-line expression via HandleCommand and capture output."""
    ret = lldb.SBCommandReturnObject()
    if language == "swift":
        debugger.GetCommandInterpreter().HandleCommand(
            f"expression -l swift -O -- {expr}", ret
        )
    else:
        debugger.GetCommandInterpreter().HandleCommand(
            f"expression -l objc -O -- {expr}", ret
        )
    if ret.Succeeded():
        return ret.GetOutput().strip()
    return None


def fetch_hierarchy_command(debugger, command, result, internal_dict):
    """LLDB command: fetch_hierarchy [output_path]

    Calls [DBGViewDebuggerSupport_iOS fetchViewHierarchy] and writes
    the resulting NSData (bplist) to the given file path.
    """
    output_path = command.strip()
    if not output_path:
        result.SetError("Usage: fetch_hierarchy <output_path>")
        return

    # Load the ViewDebuggerSupport framework
    dlopen_result = _run_expression(
        debugger,
        '(void *)dlopen("/usr/lib/libViewDebuggerSupport.dylib", 2)',
    )
    if dlopen_result is None:
        result.SetError("Failed to dlopen libViewDebuggerSupport.dylib")
        return

    # Call fetchViewHierarchy and write to file
    write_result = _run_expression(
        debugger,
        f'(BOOL)[(NSData *)[DBGViewDebuggerSupport_iOS fetchViewHierarchy] '
        f'writeToFile:@"{output_path}" atomically:YES]',
    )
    if write_result is None:
        result.SetError("Failed to call fetchViewHierarchy or write bplist")
        return

    result.AppendMessage(f"View hierarchy saved to {output_path}")


def __lldb_init_module(debugger, internal_dict):
    debugger.HandleCommand(
        "command script add -f fetch_hierarchy.fetch_hierarchy_command fetch_hierarchy"
    )
