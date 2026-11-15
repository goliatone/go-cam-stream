// Prototype-only controls. Not part of the proposed design.
import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { CloseIcon, TuneIcon } from "./icons";
import type { Sim, Snapshot } from "./sim";

export type Theme = "system" | "light" | "dark";
export interface UISettings {
  theme: Theme;
  operator: boolean;
  todayStatus: boolean;
}

const THEMES = [
  ["system", "System"],
  ["light", "Light"],
  ["dark", "Dark"],
] as const;
const CATALOGS = [
  ["normal", "Normal"],
  ["loading", "Loading"],
  ["empty", "Empty"],
  ["denied", "No access"],
  ["down", "Service down"],
] as const;

export function Harness(props: {
  sim: Sim;
  snap: Snapshot;
  ui: UISettings;
  onUI: (ui: UISettings) => void;
}) {
  const { sim, snap, ui, onUI } = props;
  const [open, setOpen] = useState(false);
  const { options, phase, selection } = snap;
  const live = phase.kind === "live" || phase.kind === "blocked";
  const session = live || phase.kind === "reconnecting";
  const selected = Boolean(selection);
  return (
    <aside class="harness" aria-label="Prototype controls">
      <div id="pt-harness" class="harness-panel" hidden={!open}>
        <div class="harness-head">
          <strong>Prototype controls</strong>
          <button
            type="button"
            class="icon-btn"
            aria-label="Close prototype controls"
            onClick={() => setOpen(false)}
          >
            <CloseIcon size={18} />
          </button>
        </div>
        <p class="harness-note">
          Synthetic video and events. No network requests. These controls are
          not part of the design.
        </p>
        <Choice
          legend="Theme"
          name="theme"
          value={ui.theme}
          options={THEMES}
          onChange={(theme) => onUI({ ...ui, theme })}
        />
        <Choice
          legend="Camera list"
          name="catalog"
          value={options.catalog}
          options={CATALOGS}
          onChange={(mode) => sim.setOption("catalog", mode)}
        />
        <fieldset class="harness-set">
          <legend>Simulate</legend>
          <div class="harness-grid">
            <Act disabled={!live} onClick={() => sim.dropConnection(true)}>
              Brief drop (recovers)
            </Act>
            <Act disabled={!live} onClick={() => sim.dropConnection(false)}>
              Drop (no recovery)
            </Act>
            <Act disabled={!session} onClick={() => sim.loseMedia()}>
              Video stops arriving
            </Act>
            <Act disabled={!selected} onClick={() => sim.force("timeout")}>
              Connect times out
            </Act>
            <Act disabled={!selected} onClick={() => sim.force("camera_busy")}>
              Camera busy
            </Act>
            <Act
              disabled={!selected}
              onClick={() => sim.force("capacity_exceeded")}
            >
              Viewer limit
            </Act>
            <Act
              disabled={!selected}
              onClick={() => sim.force("backend_unavailable")}
            >
              Video service down
            </Act>
            <Act disabled={!selected} onClick={() => sim.expire()}>
              Session lost
            </Act>
          </div>
        </fieldset>
        <fieldset class="harness-set">
          <legend>Options</legend>
          <Toggle
            label="Retries keep failing"
            checked={options.keepFailing}
            onChange={(v) => sim.setOption("keepFailing", v)}
          />
          <Toggle
            label="Autoplay blocked"
            hint="Reconnects now"
            checked={options.autoplayBlocked}
            onChange={(v) => {
              sim.setOption("autoplayBlocked", v);
              if (v) sim.force("ok");
            }}
          />
          <Toggle
            label="Status updates stop"
            checked={options.statusFrozen}
            onChange={(v) => sim.setOption("statusFrozen", v)}
          />
          <Toggle
            label="Faster timers"
            hint="All waits ÷ 4"
            checked={options.fast}
            onChange={(v) => sim.setOption("fast", v)}
          />
          <Toggle
            label="Operator actions"
            hint="Diagnostics and source reconnect"
            checked={ui.operator}
            onChange={(v) => onUI({ ...ui, operator: v })}
          />
          <Toggle
            label="Today's camera status"
            hint="Server reports every camera as unknown"
            checked={ui.todayStatus}
            onChange={(v) => onUI({ ...ui, todayStatus: v })}
          />
        </fieldset>
        <p class="harness-tip">
          Try <b>Hall</b> for a source fault. Press <kbd>?</kbd> for shortcuts.
        </p>
      </div>
      <button
        type="button"
        class="harness-toggle"
        aria-expanded={open}
        aria-controls="pt-harness"
        onClick={() => setOpen(!open)}
      >
        <TuneIcon size={18} />
        {open ? "Hide controls" : "Prototype controls"}
      </button>
    </aside>
  );
}

function Choice<T extends string>(props: {
  legend: string;
  name: string;
  value: T;
  options: readonly (readonly [T, string])[];
  onChange: (value: T) => void;
}) {
  return (
    <fieldset class="harness-set">
      <legend>{props.legend}</legend>
      <div class="chips">
        {props.options.map(([value, label]) => (
          <label key={value} class="chip">
            <input
              type="radio"
              name={`pt-harness-${props.name}`}
              value={value}
              checked={props.value === value}
              onChange={() => props.onChange(value)}
            />
            <span>{label}</span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function Toggle(props: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <label class="toggle">
      <input
        type="checkbox"
        checked={props.checked}
        onChange={(e) => props.onChange(e.currentTarget.checked)}
      />
      <span>
        {props.label}
        {props.hint ? <small>{props.hint}</small> : null}
      </span>
    </label>
  );
}

function Act(props: {
  disabled: boolean;
  onClick: () => void;
  children: ComponentChildren;
}) {
  return (
    <button
      type="button"
      class="harness-act"
      disabled={props.disabled}
      onClick={props.onClick}
    >
      {props.children}
    </button>
  );
}
