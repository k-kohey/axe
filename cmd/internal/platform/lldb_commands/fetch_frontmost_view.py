"""LLDB script to find the frontmost view controller's view address.

Writes the address (e.g. 0x10150e5a0) to a file for use by other tools.
"""

from __future__ import annotations

import re

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


def fetch_frontmost_view_command(debugger, command, result, internal_dict):
    """LLDB command: fetch_frontmost_view [output_path]

    Finds the frontmost view controller's view address and writes it to a file.
    """
    output_path = command.strip()
    if not output_path:
        result.SetError("Usage: fetch_frontmost_view <output_path>")
        return

    expr = r"""
    @import UIKit;
    UIWindowScene *scene = (UIWindowScene *)[[[UIApplication sharedApplication] connectedScenes] anyObject];
    UIViewController *vc = scene.keyWindow.rootViewController;
    UIViewController *prev = nil;
    while (vc != prev) {
        prev = vc;
        if (vc.presentedViewController != nil) {
            vc = vc.presentedViewController;
        } else if ([vc isKindOfClass:[UINavigationController class]]) {
            UIViewController *top = ((UINavigationController *)vc).topViewController;
            if (top != nil && top != vc) { vc = top; }
        } else if ([vc isKindOfClass:[UITabBarController class]]) {
            UIViewController *selected = ((UITabBarController *)vc).selectedViewController;
            if (selected != nil && selected != vc) { vc = selected; }
        } else {
            for (UIViewController *child in vc.childViewControllers) {
                if (child.viewIfLoaded != nil && child.viewIfLoaded.window != nil) {
                    vc = child;
                    break;
                }
            }
        }
    }
    [NSString stringWithFormat:@"%p", vc.view]
    """
    output = _run_expression(debugger, expr, language="objc")
    if not output:
        result.SetError("Failed to find frontmost view controller's view")
        return

    m = re.search(r"0x[0-9a-fA-F]+", output)
    if not m:
        result.SetError(f"Could not parse address from: {output}")
        return

    address = m.group(0)
    write_expr = (
        f'(BOOL)[@"{address}" writeToFile:@"{output_path}" '
        f'atomically:YES encoding:4 error:nil]'
    )
    _run_expression(debugger, write_expr)

    result.AppendMessage(f"Frontmost view: {address} (saved to {output_path})")


def __lldb_init_module(debugger, internal_dict):
    debugger.HandleCommand(
        "command script add -f fetch_frontmost_view.fetch_frontmost_view_command fetch_frontmost_view"
    )
