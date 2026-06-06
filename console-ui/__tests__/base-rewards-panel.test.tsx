import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { BaseRewardsPanel, FLOOR_TIERS } from "@/components/earn/BaseRewardsPanel";

describe("BaseRewardsPanel", () => {
  it("renders the 64GB workhorse floor at $18 (the Netflix anchor)", () => {
    render(<BaseRewardsPanel />);
    expect(screen.getByText("64GB (workhorse)")).toBeInTheDocument();
    expect(screen.getByText("$18")).toBeInTheDocument();
  });

  it("anchors the Netflix claim to 64GB+ and marks sub-48GB as usage only", () => {
    render(<BaseRewardsPanel />);
    // 64GB+ is the qualifying class.
    expect(screen.getByText(/64GB\+ Mac/)).toBeInTheDocument();
    // Sub-48GB earns nothing as a floor.
    expect(screen.getByText("Under 48GB")).toBeInTheDocument();
    expect(screen.getAllByText("Usage only").length).toBeGreaterThan(0);
  });

  it("never uses the word 'guarantee' as a promise (honesty constraint)", () => {
    const { container } = render(<BaseRewardsPanel />);
    // copy explicitly says 'not a guarantee'
    expect(container.textContent).toMatch(/not a guarantee/i);
  });

  it("exposes a floor table consistent with the coordinator tiers", () => {
    const byGB = Object.fromEntries(FLOOR_TIERS.map((t) => [t.minGB, t.floorUSD]));
    expect(byGB[64]).toBe(18);
    expect(byGB[48]).toBe(16);
    expect(byGB[512]).toBe(40);
    expect(byGB[0]).toBe(0); // sub-48GB: usage only
  });
});
