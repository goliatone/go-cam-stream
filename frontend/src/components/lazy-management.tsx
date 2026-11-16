import { useEffect, useState } from "preact/hooks";
import { createElement, type ComponentType } from "preact";
import type { ManagementDialogProps } from "./camera-management";

// One loader for optional dialogs; failed loads remain explicit and closable.
function lazyDialog<P extends object>(load: () => Promise<{default: ComponentType<P>}>, close: (props: P) => void) {
  return function Deferred(props: P) {
    const [Content, setContent] = useState<ComponentType<P>>();
    const [failed, setFailed] = useState(false);
    useEffect(() => {
      let active = true;
      void load().then(module => { if (active) setContent(() => module.default); }, () => { if (active) setFailed(true); });
      return () => { active = false; };
    }, []);
    return Content ? createElement(Content, props) : <div role="status">{failed ? "Could not load. Close and try again." : "Loading…"}<button type="button" onClick={() => close(props)}>Close</button></div>;
  };
}
export const LazyManagement = lazyDialog<ManagementDialogProps>(() => import("./camera-management"), props => props.actions.close());

export const LazyShortcuts = lazyDialog<{onClose():void}>(() => import("./viewer-shortcuts"), props => props.onClose());
