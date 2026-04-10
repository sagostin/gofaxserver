#!/usr/bin/env python3
"""
Fax Server Tenant Setup TUI

A simple terminal UI for creating new tenants on the gofaxserver.

Requirements:
    pip install prompt_toolkit requests

Environment Variables:
    FAX_API_URL   - API base URL (e.g., http://localhost:8080)
    FAX_API_KEY   - Admin API key

Usage:
    python setup-tenant-tui.py
"""

import os
import sys
import base64
import json
import requests
from typing import Optional, Tuple

try:
    from prompt_toolkit import print_formatted_text as print
    from prompt_toolkit.shortcuts import (
        yes_no_dialog,
        input_dialog,
        message_dialog,
        RadiolistDialog,
        ClearScreen,
    )
    from prompt_toolkit.shortcuts.dialogs import _create_app
    from prompt_toolkit.styles import Style
    from prompt_toolkit.formatted_text import FormattedText
except ImportError:
    print("Error: prompt_toolkit is required.")
    print("Install it with: pip install prompt_toolkit requests")
    sys.exit(1)


RED = "#ff5555"
GREEN = "#55ff55"
CYAN = "#00aaaa"
YELLOW = "#ffff00"
WHITE = "#ffffff"


class FaxServerClient:
    """Simple API client for gofaxserver."""

    def __init__(self, api_url: str, api_key: str):
        self.api_url = api_url.rstrip("/")
        self.api_key = api_key
        self._session = requests.Session()
        credentials = base64.b64encode(f"admin:{api_key}".encode()).decode()
        self._session.headers.update(
            {
                "Authorization": f"Basic {credentials}",
                "Content-Type": "application/json",
            }
        )

    def _request(self, method: str, path: str, **kwargs) -> Tuple[bool, dict, int]:
        """Make API request. Returns (success, data, status_code)."""
        url = f"{self.api_url}{path}"
        try:
            response = self._session.request(method, url, timeout=10, **kwargs)
            status = response.status_code
            try:
                data = response.json()
            except:
                data = {"raw": response.text}

            if 200 <= status < 300:
                return True, data, status
            else:
                return False, data, status
        except requests.exceptions.RequestException as e:
            return False, {"error": str(e)}, 0

    def create_tenant(self, name: str, notify: str = "") -> Tuple[bool, dict, int]:
        """Create a new tenant. Returns (success, data, status)."""
        payload = {"name": name, "notify": notify}
        return self._request("POST", "/admin/tenant", json=payload)

    def create_endpoint(
        self,
        tenant_id: int,
        endpoint_type: str,
        endpoint: str,
        bridge: bool,
        priority: int = 0,
    ) -> Tuple[bool, dict, int]:
        """Create a new endpoint."""
        payload = {
            "type": "tenant",
            "type_id": tenant_id,
            "endpoint_type": endpoint_type,
            "endpoint": endpoint,
            "priority": priority,
            "bridge": bridge,
        }
        return self._request("POST", "/admin/endpoint", json=payload)

    def create_number(
        self, tenant_id: int, number: str, name: str = "", header: str = ""
    ) -> Tuple[bool, dict, int]:
        """Add a phone number to a tenant."""
        payload = {
            "tenant_id": tenant_id,
            "number": number,
            "name": name,
            "header": header,
        }
        return self._request("POST", "/admin/number", json=payload)

    def reload(self) -> Tuple[bool, dict, int]:
        """Reload configuration."""
        return self._request("GET", "/admin/reload")


def print_header(text: str):
    """Print a header with border."""
    print(FormattedText([("fg:#00aaaa", "")]))
    print(FormattedText([("fg:#00aaaa", "╔" + "═" * 50 + "╗")]))
    for line in text.split("\n"):
        padding = 50 - len(line)
        print(FormattedText([("fg:#00aaaa", "║")]), end="")
        print(FormattedText([("fg:#00aaaa", f" {line}" + " " * padding)]), end="")
        print(FormattedText([("fg:#00aaaa", "║")]))
    print(FormattedText([("fg:#00aaaa", "╚" + "═" * 50 + "╝")]))


def print_success(text: str):
    print(FormattedText([("fg:#55ff55", text)]))


def print_error(text: str):
    print(FormattedText([("fg:#ff5555", text)]))


def print_info(text: str):
    print(FormattedText([("fg:#00aaaa", text)]))


def get_input(prompt: str, default: str = "", password: bool = False) -> Optional[str]:
    """Get text input from user."""
    try:
        from prompt_toolkit.shortcuts import prompt

        result = prompt(
            f"{prompt}: ",
            default=default,
            is_password=password,
            style=Style.from_dict(
                {
                    "prompt": "#00aaaa",
                }
            ),
        )
        return result if result else default
    except KeyboardInterrupt:
        return None


def get_yes_no(prompt: str, default: bool = False) -> Optional[bool]:
    """Get yes/no from user."""
    try:
        return yes_no_dialog(
            title="Confirm",
            text=prompt,
            default=yes_no_dialog.NO if not default else yes_no_dialog.YES,
        ).run()
    except:
        return None


def main_menu() -> str:
    """Display main menu and return choice."""
    ClearScreen.reset()
    print()
    print_header("Fax Server Tenant Setup")
    print()
    print_info("NOTE: FreeSWITCH gateway setup must be done manually.")
    print_info("      Create gateway XML in /etc/freeswitch/gateways/")
    print()
    print("  [1] Create New Tenant")
    print("  [Q] Quit")
    print()

    choice = get_input("Select option", default="1")
    if choice and choice.lower() in ["q", "quit"]:
        return "quit"
    return choice


def create_tenant_flow(client: FaxServerClient):
    """Run the create tenant flow."""
    ClearScreen.reset()
    print()
    print_header("Create New Tenant")
    print()

    # Tenant name
    tenant_name = get_input("Tenant Name")
    if not tenant_name:
        print_error("Tenant name is required")
        get_input("Press Enter to continue...")
        return

    # Gateway name
    gateway_name = get_input("Gateway Name (e.g., pbx_acme)")
    if not gateway_name:
        print_error("Gateway name is required")
        get_input("Press Enter to continue...")
        return

    # PBX IP
    pbx_ip = get_input("PBX IP Address")
    if not pbx_ip:
        print_error("PBX IP is required")
        get_input("Press Enter to continue...")
        return

    # Numbers
    numbers = get_input("Phone Numbers (comma-separated)")
    if not numbers:
        print_error("At least one phone number is required")
        get_input("Press Enter to continue...")
        return

    # Bridge mode
    print()
    bridge_mode = get_yes_no("Use Bridge Mode (transcoding)?", default=True)
    if bridge_mode is None:
        return
    if not bridge_mode:
        print_info("Using Relay Mode")
    else:
        print_info("Using Bridge Mode (transcoding)")

    # Notify (optional)
    print()
    print_info("Notify settings (optional):")
    print_info("  Examples:")
    print_info("    email_report->support@customer.com")
    print_info("    email_full_failure->fax@customer.com")
    print_info("    webhook_form->https://n8n.example.com/webhook/123")
    notify = get_input("Notify (leave blank for none)", default="")
    if not notify:
        notify = ""

    # Review
    print()
    print_header("Review")
    print()
    print(f"  Tenant Name:  {tenant_name}")
    print(f"  Gateway Name: {gateway_name}")
    print(f"  PBX IP:       {pbx_ip}")
    print(f"  Mode:         {'Bridge (transcoding)' if bridge_mode else 'Relay'}")
    print(f"  Numbers:      {numbers}")
    print(f"  Notify:       {notify or '(none)'}")
    print()

    confirm = get_yes_no("Create tenant?", default=True)
    if not confirm:
        print_info("Cancelled")
        get_input("Press Enter to continue...")
        return

    # Create tenant
    print()
    print_info("Creating tenant...")

    success, data, status = client.create_tenant(tenant_name, notify)
    if not success:
        print_error(f"Failed to create tenant: {data.get('error', str(data))}")
        get_input("Press Enter to continue...")
        return

    tenant_id = data.get("id")
    if not tenant_id:
        print_error("No tenant ID returned")
        get_input("Press Enter to continue...")
        return

    print_success(f"Tenant created (ID: {tenant_id})")

    # Create endpoint
    endpoint_str = f"{gateway_name}:{pbx_ip}"
    print_info(f"Creating endpoint: {endpoint_str}...")

    success, data, status = client.create_endpoint(
        tenant_id=tenant_id,
        endpoint_type="gateway",
        endpoint=endpoint_str,
        bridge=bridge_mode,
    )
    if not success:
        print_error(f"Failed to create endpoint: {data.get('error', str(data))}")
        get_input("Press Enter to continue...")
        return

    print_success("Endpoint created")

    # Create numbers
    number_list = [n.strip() for n in numbers.split(",") if n.strip()]
    created_numbers = []
    failed_numbers = []

    for num in number_list:
        print_info(f"Creating number: {num}...")
        success, data, status = client.create_number(
            tenant_id=tenant_id, number=num, name=tenant_name, header=tenant_name
        )
        if success:
            created_numbers.append(num)
            print_success(f"  Number {num} created")
        else:
            failed_numbers.append(num)
            print_error(
                f"  Failed to create number {num}: {data.get('error', str(data))}"
            )

    # Reload
    print_info("Reloading configuration...")
    client.reload()
    print_success("Configuration reloaded")

    # Summary
    print()
    print_header("Success!")
    print()
    print_success(f"  Tenant ID:  {tenant_id}")
    print_success(
        f"  Endpoint:   {endpoint_str} ({'bridge' if bridge_mode else 'relay'})"
    )
    print_success(f"  Numbers:    {', '.join(created_numbers)}")
    if failed_numbers:
        print_error(f"  Failed:     {', '.join(failed_numbers)}")
    print()
    print_info("Remember to:")
    print_info("  1. Create/verify FreeSWITCH gateway XML in /etc/freeswitch/gateways/")
    print_info("  2. Run 'sofia profile fax rescan' in fs_cli")
    print()

    get_input("Press Enter to return to menu...")


def main():
    # Check environment
    api_url = os.environ.get("FAX_API_URL", "")
    api_key = os.environ.get("FAX_API_KEY", "")

    if not api_url:
        print_error("FAX_API_URL environment variable not set")
        api_url = get_input("Enter API URL", default="http://localhost:8080")
        if not api_url:
            sys.exit(1)

    if not api_key:
        print_error("FAX_API_KEY environment variable not set")
        api_key = get_input("Enter API Key", password=True)
        if not api_key:
            sys.exit(1)

    # Create client
    client = FaxServerClient(api_url, api_key)

    # Test connection
    print_info("Testing API connection...")
    success, data, status = client.reload()
    if not success:
        print_error(f"Failed to connect to API: {data.get('error', str(data))}")
        get_input("Press Enter to exit...")
        sys.exit(1)
    print_success("Connected successfully")

    # Main loop
    while True:
        choice = main_menu()

        if choice == "quit" or choice is None:
            break
        elif choice == "1":
            create_tenant_flow(client)
        else:
            print_error("Invalid option")
            get_input("Press Enter to continue...")


if __name__ == "__main__":
    main()
