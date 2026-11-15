import { useEffect, useState } from "preact/hooks";
import type { Sim, Snapshot } from "./sim";

export function useSnapshot(sim: Sim): Snapshot {
  const [snapshot, setSnapshot] = useState(() => sim.get());
  useEffect(() => {
    setSnapshot(sim.get());
    return sim.subscribe(() => setSnapshot(sim.get()));
  }, [sim]);
  return snapshot;
}

export function useNow(interval: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), interval);
    return () => clearInterval(timer);
  }, [interval]);
  return now;
}

export function useMedia(query: string) {
  const [matches, setMatches] = useState(() => matchMedia(query).matches);
  useEffect(() => {
    const list = matchMedia(query);
    const update = () => setMatches(list.matches);
    update();
    list.addEventListener("change", update);
    return () => list.removeEventListener("change", update);
  }, [query]);
  return matches;
}
