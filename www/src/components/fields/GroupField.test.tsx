// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { FieldSchema } from "../../registry/types";
import GroupField from "./GroupField";

const rulesField: FieldSchema = {
  key: "rules",
  label: "Rules",
  type: "group",
  fields: [
    { key: "action", label: "Action", type: "enum", enumValues: ["allow", "deny"] },
    { key: "port", label: "Port", type: "number" },
  ],
};

describe("GroupField", () => {
  it("adds a row with an empty object", () => {
    const onChange = vi.fn();
    render(<GroupField id="f" field={rulesField} value={[]} onChange={onChange} />);

    fireEvent.click(screen.getByText("Add rules"));
    expect(onChange).toHaveBeenCalledWith([{}]);
  });

  it("renders each row's sub-fields and updates one in place", () => {
    const onChange = vi.fn();
    render(
      <GroupField
        id="f"
        field={rulesField}
        value={[{ action: "allow", port: 443 }]}
        onChange={onChange}
      />,
    );

    screen.getByDisplayValue("allow");
    const portInput = screen.getByDisplayValue("443");
    fireEvent.change(portInput, { target: { value: "8080" } });

    expect(onChange).toHaveBeenCalledWith([{ action: "allow", port: 8080 }]);
  });

  it("removes a row", () => {
    const onChange = vi.fn();
    render(
      <GroupField
        id="f"
        field={rulesField}
        value={[{ action: "allow" }, { action: "deny" }]}
        onChange={onChange}
      />,
    );

    fireEvent.click(screen.getByLabelText("Remove row 1"));
    expect(onChange).toHaveBeenCalledWith([{ action: "deny" }]);
  });

  it("updates one row in place, leaving the others untouched", () => {
    const onChange = vi.fn();
    render(
      <GroupField
        id="f"
        field={rulesField}
        value={[{ action: "allow", port: 80 }, { action: "deny", port: 443 }]}
        onChange={onChange}
      />,
    );

    fireEvent.change(screen.getByDisplayValue("443"), { target: { value: "8443" } });

    expect(onChange).toHaveBeenCalledWith([
      { action: "allow", port: 80 },
      { action: "deny", port: 8443 },
    ]);
  });

  it("renders no sub-fields when the schema omits them", () => {
    const noSubFields: FieldSchema = { key: "rules", label: "Rules", type: "group" };
    const onChange = vi.fn();
    render(<GroupField id="f" field={noSubFields} value={[{ anything: "x" }]} onChange={onChange} />);

    fireEvent.click(screen.getByText("Add rules"));
    expect(onChange).toHaveBeenCalledWith([{ anything: "x" }, {}]);
  });
});
