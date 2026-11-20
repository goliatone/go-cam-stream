import { render } from "preact";
import { createGuestAPI } from "./guest-api";
import { bindPageEvents, browserEnvironment, GuestSession } from "./guest-session";
import { GuestViewer } from "./guest-viewer";
import "./guest.css";

// Standalone guest entry for /s/{share_id}. It shares no code path with the
// operator viewer and keeps no state outside this tab's memory.
const session = new GuestSession(browserEnvironment(), (shareID) => createGuestAPI(shareID));
bindPageEvents(session);
render(<GuestViewer session={session} />, document.getElementById("guest")!);
session.start();
