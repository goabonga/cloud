// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { FieldSchema } from "../../registry/types";
import FieldInput from "./FieldInput";

function field(overrides: Partial<FieldSchema>): FieldSchema {
  return { key: "x", label: "X", type: "string", ...overrides };
}

describe("FieldInput", () => {
  it("renders a string field and reports changes", () => {
    const onChange = vi.fn();
    render(<FieldInput id="f" field={field({ type: "string" })} value="hello" onChange={onChange} />);
    const input = screen.getByDisplayValue("hello");
    fireEvent.change(input, { target: { value: "world" } });
    expect(onChange).toHaveBeenCalledWith("world");
  });

  it("renders a text field as a multi-line textarea", () => {
    const onChange = vi.fn();
    render(<FieldInput id="f" field={field({ type: "text" })} value={"line 1\nline 2"} onChange={onChange} />);
    const textarea = screen.getByRole("textbox");
    expect(textarea.tagName).toBe("TEXTAREA");
    fireEvent.change(textarea, { target: { value: "#cloud-config\n" } });
    expect(onChange).toHaveBeenCalledWith("#cloud-config\n");
  });

  it("renders a number field as a number input and parses numeric changes", () => {
    const onChange = vi.fn();
    render(<FieldInput id="f" field={field({ type: "number" })} value={4} onChange={onChange} />);
    const input = screen.getByDisplayValue("4");
    fireEvent.change(input, { target: { value: "8" } });
    expect(onChange).toHaveBeenCalledWith(8);
  });

  it("clears a number field to undefined when emptied", () => {
    const onChange = vi.fn();
    render(<FieldInput id="f" field={field({ type: "number" })} value={4} onChange={onChange} />);
    fireEvent.change(screen.getByDisplayValue("4"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith(undefined);
  });

  it("renders a boolean field as a checkbox", () => {
    const onChange = vi.fn();
    render(<FieldInput id="f" field={field({ type: "boolean" })} value={false} onChange={onChange} />);
    const checkbox = screen.getByRole("checkbox") as HTMLInputElement;
    expect(checkbox.checked).toBe(false);
    fireEvent.click(checkbox);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("renders an enum field with its options", () => {
    const onChange = vi.fn();
    render(
      <FieldInput id="f" field={field({ type: "enum", enumValues: ["allow", "deny"] })} value="allow" onChange={onChange} />,
    );
    fireEvent.change(screen.getByDisplayValue("allow"), { target: { value: "deny" } });
    expect(onChange).toHaveBeenCalledWith("deny");
  });

  it("adds and removes rows in a stringList field", () => {
    const onChange = vi.fn();
    const { rerender } = render(<FieldInput id="f" field={field({ type: "stringList" })} value={["a"]} onChange={onChange} />);

    fireEvent.click(screen.getByText("Add value"));
    expect(onChange).toHaveBeenLastCalledWith(["a", ""]);

    rerender(<FieldInput id="f" field={field({ type: "stringList" })} value={["a", "b"]} onChange={onChange} />);
    fireEvent.click(screen.getAllByLabelText("Remove value")[0]);
    expect(onChange).toHaveBeenLastCalledWith(["b"]);
  });

  it("adds and edits entries in a keyValue field", () => {
    const onChange = vi.fn();
    const { rerender } = render(<FieldInput id="f" field={field({ type: "keyValue" })} value={[]} onChange={onChange} />);

    fireEvent.click(screen.getByText("Add entry"));
    expect(onChange).toHaveBeenLastCalledWith([{ key: "", value: "" }]);

    rerender(
      <FieldInput id="f" field={field({ type: "keyValue" })} value={[{ key: "", value: "" }]} onChange={onChange} />,
    );
    fireEvent.change(screen.getByPlaceholderText("key"), { target: { value: "tier" } });
    expect(onChange).toHaveBeenLastCalledWith([{ key: "tier", value: "" }]);
  });
});
