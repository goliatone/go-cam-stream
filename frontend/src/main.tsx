import { render } from "preact";
import { App } from "./app";
import { useViewer, viewerAPI } from "./hooks/use-viewer";
import "./styles.css";
import "./components/camera-management.css";

function LiveViewer() { return <App {...useViewer(viewerAPI)} />; }
render(<LiveViewer />, document.getElementById("app")!);
