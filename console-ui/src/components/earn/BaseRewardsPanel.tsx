"use client";

import { Shield, TrendingUp, Clock, Info } from "lucide-react";

/**
 * BaseRewardsPanel — provider-facing explainer for the base-rewards earnings
 * floor on the /earn page.
 *
 * The model: payout = max(usage_earnings, floor). The floor is what a machine
 * earns when the network is quiet; real inference is the upside (out-earn the
 * floor and you keep 100%). The floor is reduced dollar-for-dollar by what you
 * earn, so the subsidy targets idle machines and disappears as demand grows.
 *
 * Honesty constraints (see docs/base-rewards.md): the "covers your Netflix"
 * line applies to 64GB+ machines only; sub-48GB earns from usage only; we never
 * call it a "guarantee" (it is eligibility-gated and slot-capped).
 */

export interface FloorTier {
  /** Lower bound of the memory tier, in GB. */
  minGB: number;
  /** Human label for the tier. */
  label: string;
  /** Worst-case monthly floor in USD (0 = usage only). */
  floorUSD: number;
  /** How the tier maps to the "Netflix" framing. */
  netflix: "standard" | "ads" | "none";
}

/** The base-rewards floor table (worst case, full eligibility). Mirrors the
 *  tiers in coordinator/payments/baserewards/floor.go. */
export const FLOOR_TIERS: FloorTier[] = [
  { minGB: 512, label: "512GB", floorUSD: 40, netflix: "standard" },
  { minGB: 192, label: "192GB Mac Studio", floorUSD: 30, netflix: "standard" },
  { minGB: 128, label: "128GB Ultra", floorUSD: 26, netflix: "standard" },
  { minGB: 96, label: "96GB", floorUSD: 22, netflix: "standard" },
  { minGB: 64, label: "64GB (workhorse)", floorUSD: 18, netflix: "standard" },
  { minGB: 48, label: "48GB", floorUSD: 16, netflix: "ads" },
  { minGB: 0, label: "Under 48GB", floorUSD: 0, netflix: "none" },
];

function netflixLabel(n: FloorTier["netflix"]): string {
  switch (n) {
    case "standard":
      return "Netflix Standard";
    case "ads":
      return "Netflix w/ ads";
    default:
      return "Usage only";
  }
}

function netflixColor(n: FloorTier["netflix"]): string {
  switch (n) {
    case "standard":
      return "text-accent-green";
    case "ads":
      return "text-accent-amber";
    default:
      return "text-text-tertiary";
  }
}

export function BaseRewardsPanel() {
  return (
    <div className="rounded-xl bg-bg-secondary p-6 mb-6">
      <div className="flex items-center gap-2 mb-2">
        <Shield size={18} className="text-accent-brand" />
        <h3 className="text-sm font-semibold text-text-primary">Earnings floor</h3>
      </div>
      <p className="text-sm text-text-secondary mb-5">
        Run a <span className="text-text-primary font-medium">64GB+ Mac</span> and even when the
        network is quiet, you earn at least a Netflix subscription — best case, more. Smaller Macs
        earn from real usage. We pay the <span className="text-text-primary font-medium">greater</span>{" "}
        of your usage earnings or your floor, so the floor shrinks as you earn and vanishes once you
        out-earn it.
      </p>

      <div className="overflow-hidden rounded-lg border border-border-subtle">
        <table className="w-full text-sm">
          <thead>
            <tr className="bg-bg-tertiary text-text-tertiary">
              <th className="text-left font-medium px-4 py-2">Unified memory</th>
              <th className="text-right font-medium px-4 py-2">Floor / mo</th>
              <th className="text-right font-medium px-4 py-2">Worst case</th>
            </tr>
          </thead>
          <tbody>
            {FLOOR_TIERS.map((t) => (
              <tr key={t.minGB} className="border-t border-border-subtle">
                <td className="px-4 py-2 text-text-secondary">{t.label}</td>
                <td className="px-4 py-2 text-right font-mono text-text-primary">
                  {t.floorUSD > 0 ? `$${t.floorUSD}` : "—"}
                </td>
                <td className="px-4 py-2 text-right">
                  <span className={netflixColor(t.netflix)}>{netflixLabel(t.netflix)}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="mt-4 space-y-2">
        <div className="flex items-start gap-2 text-xs text-text-tertiary">
          <Clock size={13} className="shrink-0 mt-0.5" />
          <span>Requires staying online ≥90% of the month (the floor ramps with uptime).</span>
        </div>
        <div className="flex items-start gap-2 text-xs text-text-tertiary">
          <TrendingUp size={13} className="shrink-0 mt-0.5" />
          <span>
            Out-earn your floor and you keep 100% of usage — the floor only ever tops you up.
          </span>
        </div>
        <div className="flex items-start gap-2 text-xs text-text-tertiary">
          <Info size={13} className="shrink-0 mt-0.5" />
          <span>
            Floors are allocated to attested, actively-serving machines up to a fixed monthly
            budget; not a guarantee. See the docs for eligibility.
          </span>
        </div>
      </div>
    </div>
  );
}
