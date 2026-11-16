import { render, type ComponentType } from "preact";
import { useEffect, useState } from "preact/hooks";
import type { ManagementHostProps } from "./hooks/use-management";
import { App, type AppProps } from "./app";
import { useViewer, viewerAPI } from "./hooks/use-viewer";
import "./styles.css";
import "./components/camera-management.css";

function LiveViewer() {
  const props=useViewer(viewerAPI);
  const [management,setManagementProps]=useState<Partial<AppProps>>({});
  const [Management,setManagement]=useState<ComponentType<ManagementHostProps>>();
  useEffect(()=>{let active=true;void import("./hooks/use-management").then(module=>{if(active)setManagement(()=>module.default)},()=>{});return()=>{active=false}},[]);
  return <><App {...props} {...management}/>{Management&&<Management viewer={props} refresh={props.refreshCatalog} recovered={props.onRecovered} onChange={setManagementProps}/>}</>;
}
render(<LiveViewer />, document.getElementById("app")!);
