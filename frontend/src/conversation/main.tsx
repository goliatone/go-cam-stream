import { render } from "preact";
import { nativeCall } from "./bridge";
import { Conversation } from "./conversation";
import { readHostHandoff, readInvitation } from "./invitation";
import "./conversation.css";

const native = nativeCall();
const location = new URL(window.location.href);
const invitation = readInvitation(location);
const host = readHostHandoff(location);
if (location.hash || location.search) history.replaceState(null, "", location.pathname);
const props = { invitation, native, host };
render(<Conversation {...props} />, document.getElementById("conversation")!);
