import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import CameraManagementDialog, { type ManagementActions, type ManagementView } from "./camera-management";
import type { CameraSettingsContext } from "./settings-drawer";
import { CameraRecovery } from "./camera-recovery";
import { App } from "../app";
import type { CameraSettings, DraftReview, ManagementErrorCode, TestResult } from "../services/contracts";

afterEach(cleanup);
const actions = (): ManagementActions => ({submit:vi.fn(),importFiles:vi.fn(),test:vi.fn(),cancelTest:vi.fn(),save:vi.fn(),recover:vi.fn(),cancel:vi.fn(),close:vi.fn(),watch:vi.fn(),reset:vi.fn()});
const draft: DraftReview = {id:"draft",candidate_id:"camera",revision:1,runtime_epoch:"epoch",expires_at:"2030-01-01T00:00:00Z",name:"Mac",endpoint:"rtsp://localhost:18556/macbook",authentication:"credentials",credentials_present:true,warnings:["server_relative_localhost","imported_grants_ignored","creator_only_access"],state:"ready"};
const view: ManagementView = {phase:"editing",storageReady:true};
const running: TestResult = {id:"test",attempt_key:"attempt",draft_revision:1,stage:"source_media",expires_at:draft.expires_at,source_media_observed:false,browser_frames_reported:false,cleanup:"pending"};
const failedTest = (code: ManagementErrorCode, extra: Partial<TestResult> = {}): TestResult => ({...running,stage:"failed",cleanup:"confirmed",error:{code,message:"untrusted backend detail",retryable:true},...extra});
const tested = {...draft,state:"tested" as const,test:{...running,stage:"passed" as const,source_media_observed:true,browser_frames_reported:true,cleanup:"confirmed" as const}};
const saved = {camera_id:"new",catalog_revision:{runtime_epoch:"epoch",generation:2}};
const footer = () => [...document.querySelectorAll(".management-dialog .dialog-inner > .dialog-actions button")].map(b => b.textContent);
const steps = () => [...document.querySelectorAll(".stepper li")].map(li => `${li.textContent}${li.hasAttribute("aria-current") ? " (current)" : ""}`);
const checklist = () => [...document.querySelectorAll(".checklist li")].map(li => li.textContent);
const zone = () => screen.getByText(/Drop the exported/).parentElement!;
const picker = () => zone().querySelector<HTMLInputElement>("input[type=file]")!;

it("offers managers a first camera by connection type and tells viewers whom to ask", () => {
 const {rerender}=render(<App catalog={{cameras:[]}} />);
 expect(screen.getByRole("heading",{name:"No cameras yet"})).toBeTruthy();
 expect(screen.queryByRole("button",{name:"Network camera (RTSP)"})).toBeNull();expect(screen.queryByRole("button",{name:"Add Camera"})).toBeNull();
 const add=vi.fn();rerender(<App catalog={{cameras:[]}} onAddCamera={add}/>);
 expect(screen.getByRole("heading",{name:"Add your first camera"})).toBeTruthy();expect(screen.queryByText(/Ask the person who runs camstream/)).toBeNull();
 fireEvent.click(screen.getByRole("button",{name:"Network camera (RTSP)"}));expect(add).toHaveBeenLastCalledWith("manual");
 fireEvent.click(screen.getByRole("button",{name:"Camstream Mac app"}));expect(add).toHaveBeenLastCalledWith("import");
});
it("transfers manual credentials once and clears the input fields", async () => {
 const user=userEvent.setup();const a=actions();render(<CameraManagementDialog view={view} actions={a}/>);
 expect((screen.getByRole("switch",{name:"Requires sign-in"}) as HTMLInputElement).checked).toBe(true);
 await user.type(screen.getByLabelText("Camera name"),"Mac");await user.type(screen.getByLabelText("RTSP address"),draft.endpoint);
 await user.type(screen.getByLabelText("Username"),"camera-user");await user.type(screen.getByLabelText("Password"),"PRIVATE-PASSWORD");
 await user.click(screen.getByRole("button",{name:"Continue"}));
 expect(a.submit).toHaveBeenCalledWith({name:"Mac",endpoint:draft.endpoint,authentication:"credentials",credentials:{username:"camera-user",password:"PRIVATE-PASSWORD"}});
 expect((screen.getByLabelText("Password") as HTMLInputElement).value).toBe("");
 expect((screen.getByLabelText("Username") as HTMLInputElement).value).toBe("");
});
it("asks for anonymous access only when sign-in is switched off, and says the Mac app needs it", async () => {
 const user=userEvent.setup();const a=actions();render(<CameraManagementDialog view={view} actions={a}/>);
 await user.type(screen.getByLabelText("Camera name"),"Door");await user.type(screen.getByLabelText("RTSP address"),"rtsp://door.lan:554/live");
 await user.click(screen.getByRole("switch",{name:"Requires sign-in"}));
 expect(screen.queryByLabelText("Username")).toBeNull();expect(screen.queryByLabelText("Password")).toBeNull();
 expect(screen.getByText(/Camstream Mac app, which always requires sign-in/)).toBeTruthy();
 await user.click(screen.getByRole("button",{name:"Continue"}));
 expect(a.submit).toHaveBeenCalledWith({name:"Door",endpoint:"rtsp://door.lan:554/live",authentication:"anonymous"});
});
it("notes localhost only for a local address and checks the address once the field is left", async () => {
 const user=userEvent.setup();render(<CameraManagementDialog view={view} actions={actions()}/>);
 const address=screen.getByLabelText("RTSP address");const local=()=>screen.queryByText(/^Localhost is the server’s own machine/);
 await user.type(address,"rtsp://user:secret@door.lan/live");expect(address.getAttribute("aria-invalid")).toBeNull();
 await user.tab();expect(address.getAttribute("aria-invalid")).toBe("true");
 expect(screen.getByText("Use rtsp://host:port/path, without a username or password.")).toBeTruthy();
 await user.clear(address);await user.type(address,"rtsp://door.lan:554/live");
 expect(address.getAttribute("aria-invalid")).toBeNull();expect(local()).toBeNull();
 for(const host of ["localhost:18556","127.0.0.1:8554","[[::1]"]){await user.clear(address);await user.type(address,`rtsp://${host}/cam`);expect(local(),host).toBeTruthy();}
});
it("starts the check after Continue, never for a restored draft or an import", async () => {
 const user=userEvent.setup();const a=actions();const {rerender}=render(<CameraManagementDialog view={view} actions={a}/>);
 await user.type(screen.getByLabelText("Camera name"),"Mac");await user.type(screen.getByLabelText("RTSP address"),draft.endpoint);
 await user.type(screen.getByLabelText("Username"),"user");await user.type(screen.getByLabelText("Password"),"secret");
 await user.click(screen.getByRole("button",{name:"Continue"}));
 rerender(<CameraManagementDialog view={{...view,busy:true,unresolved:true}} actions={a}/>);expect(a.test).not.toHaveBeenCalled();
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft}} actions={a}/>);
 await waitFor(()=>expect(a.test).toHaveBeenCalledWith(screen.getByLabelText("Camera preview")));
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:failedTest("camera_unreachable")}} actions={a}/>);
 expect(a.test).toHaveBeenCalledOnce();cleanup();
 const restored=actions();render(<CameraManagementDialog view={{...view,phase:"review",draft}} actions={restored}/>);
 expect(screen.getByRole("button",{name:"Check connection"})).toBeTruthy();expect(restored.test).not.toHaveBeenCalled();cleanup();
 const imported=actions();const yaml=new File(["camera data"],"camstream-camera.yaml"),env=new File(["user"],"camstream-camera.env");
 const second=render(<CameraManagementDialog view={view} actions={imported} mode="import"/>);
 fireEvent.drop(zone(),{dataTransfer:{files:[yaml,env]}});await user.click(screen.getByRole("button",{name:"Continue"}));
 expect(imported.importFiles).toHaveBeenCalledWith(yaml,env);
 second.rerender(<CameraManagementDialog view={{...view,phase:"review",draft}} actions={imported} mode="import"/>);
 await waitFor(()=>expect(screen.getByRole("button",{name:"Check connection"})).toBeTruthy());expect(imported.test).not.toHaveBeenCalled();
});
it("walks Connect, Check and Save and summarises the camera, including its selected address", () => {
 const a=actions();const {rerender}=render(<CameraManagementDialog view={view} actions={a}/>);
 expect(steps()).toEqual(["Connect (current)","Check","Save"]);
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft}} actions={a}/>);expect(steps()).toEqual(["Connect (done)","Check (current)","Save"]);
 expect(screen.queryByLabelText("Password")).toBeNull();
 const pending={...tested,dial_address:"192.168.1.20:18556",test:{...tested.test,cleanup:"pending" as const}};
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft:pending,test:pending.test}} actions={a}/>);
 expect(steps()).toEqual(["Connect (done)","Check (done)","Save (current)"]);
 expect((screen.getByRole("button",{name:"Save camera"}) as HTMLButtonElement).disabled).toBe(true);
 const summary=within(screen.getByRole("region",{name:"Camera summary"}));
 expect(summary.getByRole("heading",{name:"Mac"})).toBeTruthy();
 expect([...document.querySelectorAll(".management-review dt")].map(dt=>`${dt.textContent}: ${dt.nextElementSibling?.textContent}`)).toEqual([
  `Address: ${draft.endpoint}`,"Connects to: 192.168.1.20:18556","Sign-in: Username and password (hidden)","Video: Default stream, no audio","Visible to: Only you"]);
 expect(summary.getAllByRole("listitem").map(li=>li.textContent)).toEqual([expect.stringMatching(/^Localhost is the server’s own machine/),"Sharing settings in the file are not imported."]);
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft:{...pending,test:tested.test}}} actions={a}/>);
 expect(screen.getByRole("status").textContent).toBe("Connection works. Ready to save.");
 fireEvent.click(screen.getByRole("button",{name:"Save camera"}));expect(a.save).toHaveBeenCalledOnce();
});
it("shows the check's progress, with Stop check, and Play preview once video arrives", () => {
 const a=actions();const checking={...view,phase:"testing" as const,draft:{...draft,state:"testing" as const}};
 const {rerender}=render(<CameraManagementDialog view={{...checking,test:running}} actions={a}/>);
 expect(checklist()).toEqual(["Reaching the camera: checking","Receiving video: waiting","Playing in this browser: waiting"]);
 expect(screen.getByRole("status").textContent).toBe("Checking the connection…");
 expect(footer()).toEqual(["Stop check","Edit details","Cancel"]);expect((screen.getByRole("button",{name:"Edit details"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"Stop check"}));expect(a.cancelTest).toHaveBeenCalledOnce();
 rerender(<CameraManagementDialog view={{...checking,test:{...running,stage:"browser_playback",source_media_observed:true}}} actions={a}/>);
 expect(checklist()).toEqual(["Reaching the camera: done","Receiving video: done","Playing in this browser: checking"]);
 expect(footer()).toEqual(["Stop check","Play preview","Edit details","Cancel"]);
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:{...running,stage:"canceled",cleanup:"confirmed"}}} actions={a}/>);
 expect(screen.getByText("The check stopped before it finished.")).toBeTruthy();expect(footer()[0]).toBe("Check again");
});
it("names the failed stage, offers Check again and Edit details, and keeps the code under Details", async () => {
 const user=userEvent.setup();const a=actions();const anonymous={...draft,authentication:"anonymous" as const,credentials_present:false};
 const {rerender}=render(<CameraManagementDialog view={{...view,phase:"review",draft:anonymous,test:failedTest("source_unavailable")}} actions={a}/>);
 expect(checklist()).toEqual(["Reaching the camera: waiting","Receiving video: failed","Playing in this browser: waiting"]);
 expect(screen.getByRole("alert").textContent).toMatch(/username and password are right/);expect(screen.getByRole("alert").textContent).not.toMatch(/rejected/);
 expect(screen.queryByText(/untrusted backend detail/)).toBeNull();
 expect(screen.getByText("source_unavailable").closest("details")?.querySelector("summary")?.textContent).toBe("Details");
 expect(screen.queryByText("Not required")).toBeNull();expect(screen.getByText("Not provided (anonymous access)")).toBeTruthy();
 expect(screen.getByText(/The Camstream Mac app always requires it/)).toBeTruthy();
 expect(footer()).toEqual(["Check again","Edit details","Cancel"]);
 fireEvent.click(screen.getByRole("button",{name:"Check again"}));expect(a.test).toHaveBeenCalledWith(screen.getByLabelText("Camera preview"));
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:failedTest("authentication_failed")}} actions={a}/>);
 expect(checklist()[0]).toBe("Reaching the camera: failed");expect(screen.getByRole("alert").textContent).toMatch(/rejected the username or password/);
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:failedTest("media_connection_failed",{source_media_observed:true})}} actions={a}/>);
 expect(checklist()).toEqual(["Reaching the camera: done","Receiving video: done","Playing in this browser: failed"]);
 expect(screen.getByRole("alert").textContent).toContain("WebRTC media address and ports");
 await user.click(screen.getByRole("button",{name:"Edit details"}));
 expect(steps()[0]).toBe("Connect (current)");expect((screen.getByLabelText("RTSP address") as HTMLInputElement).value).toBe(draft.endpoint);
 await waitFor(()=>expect(document.activeElement).toBe(screen.getByLabelText("Camera name")));
 expect(footer()).toEqual(["Update details","Back","Cancel"]);
 await user.click(screen.getByRole("button",{name:"Back"}));expect(steps()[1]).toBe("Check (current)");
});
it("distinguishes check timeouts from an expired pass, which returns to Check", () => {
 const timeout=failedTest("test_expired",{source_media_observed:true});
 const {rerender}=render(<CameraManagementDialog view={{...view,phase:"review",draft,test:timeout}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toMatch(/video reached the server, but this browser didn’t play it/);
 expect(screen.getByRole("alert").textContent).toContain("--media-address");
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:{...timeout,source_media_observed:false}}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toMatch(/timed out before the camera sent video/);expect(screen.getByRole("alert").textContent).not.toContain("Firefox");
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:{...timeout,browser_frames_reported:true}}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toBe("Check this camera again before saving.");expect(screen.queryByText(/untrusted backend detail/)).toBeNull();
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft:tested,test:tested.test,error:"test_expired"}} actions={actions()}/>);
 expect(steps()).toEqual(["Connect (done)","Check (current)","Save"]);expect(screen.queryByRole("button",{name:"Save camera"})).toBeNull();
 expect(footer()).toEqual(["Check again","Edit details","Cancel"]);
});
it("locks changes without site storage, and replaces the steps while an outcome is unknown", () => {
 const a=actions();const {rerender}=render(<CameraManagementDialog view={{...view,storageReady:false}} actions={a}/>);
 expect(screen.getByRole("alert").textContent).toMatch(/Allow site storage/);
 expect((screen.getByRole("button",{name:"Continue"}) as HTMLButtonElement).disabled).toBe(true);expect(screen.getByLabelText("Camera name").closest("fieldset")?.disabled).toBe(true);
 rerender(<CameraManagementDialog view={{...view,phase:"recovering",busy:true,unresolved:true,checkingSave:true}} actions={a}/>);
 expect(screen.getByRole("status").textContent).toBe("Checking whether your camera was saved…");expect(footer()).toEqual([]);
 rerender(<CameraManagementDialog view={{...view,phase:"recovering",draft,unresolved:true,error:"save_outcome_unknown",canRetrySave:true}} actions={a}/>);
 expect(screen.queryByRole("list",{name:"Steps"})).toBeNull();expect(screen.queryByRole("region",{name:"Camera summary"})).toBeNull();
 expect(screen.getByRole("alert").textContent).toBe("Your camera may already be saved. Check again before making changes.");
 expect(footer()).toEqual(["Check again","Try saving again"]);
 fireEvent.click(screen.getByRole("button",{name:"Check again"}));expect(a.recover).toHaveBeenCalledOnce();
 // A lookup after a change in this dialog keeps the steps, locked.
 rerender(<CameraManagementDialog view={{...view,phase:"recovering",draft,busy:true,unresolved:true}} actions={a}/>);
 expect(screen.getByRole("status").textContent).toBe("Checking your last change…");expect(screen.getByRole("list",{name:"Steps"})).toBeTruthy();
 expect((screen.getByRole("button",{name:"Check connection"}) as HTMLButtonElement).disabled).toBe(true);
});
it("offers a terminal message and close for an expired save", () => {
 const a=actions();
 render(<CameraManagementDialog view={{...view,phase:"canceled",error:"receipt_expired"}} actions={a}/>);
 expect(screen.getByRole("heading",{name:"Camera save"})).toBeTruthy();expect(screen.getByRole("alert").textContent).toMatch(/can’t be confirmed anymore/);
 expect(footer()).toEqual(["Close"]);
 fireEvent.click(screen.getByRole("button",{name:"Close"}));
 expect(a.close).toHaveBeenCalledOnce();
});
it("recognises dropped or chosen export files, checks each one and clears them after import", async () => {
 const user=userEvent.setup();const a=actions();render(<CameraManagementDialog view={view} actions={a}/>);
 await user.click(screen.getByRole("radio",{name:"Camstream Mac app"}));
 expect(screen.getByText(/Connection Details → Export Connection Files → Show Files in Finder/).closest("details")?.querySelector("summary")?.textContent).toBe("Where do I find these?");
 await user.click(screen.getByRole("button",{name:"Continue"}));
 expect(screen.getByRole("alert").textContent).toBe("Add the exported .yaml file.");expect(a.importFiles).not.toHaveBeenCalled();
 fireEvent.change(picker(),{target:{files:[new File(["x".repeat(40000)],"large.yaml")]}});
 expect(screen.getByRole("alert").textContent).toBe("large.yaml is too large for a Camstream export.");
 fireEvent.change(picker(),{target:{files:[new File(["notes"],"notes.txt")]}});
 expect(screen.getByRole("alert").textContent).toBe("notes.txt is not a Camstream export file.");
 const yaml=new File(["camera data"],"camstream-camera.yaml",{type:"text/yaml"});
 fireEvent.drop(zone(),{dataTransfer:{files:[yaml]}});
 expect(screen.getByText("camstream-camera.yaml · connection")).toBeTruthy();expect(screen.queryByRole("alert")).toBeNull();
 expect(screen.getByText(/Without the .env file, you’ll enter the username and password next/)).toBeTruthy();
 const env=new File(["user"],"camstream-camera.env");fireEvent.change(picker(),{target:{files:[env]}});
 expect(screen.getByText("camstream-camera.env · sign-in")).toBeTruthy();
 await user.click(screen.getByRole("button",{name:"Continue"}));
 expect(a.importFiles).toHaveBeenCalledWith(yaml,env);expect(screen.queryByText(/· connection/)).toBeNull();
});
it("Watch is explicit and source reconnect requires impact confirmation", async () => {
 const a=actions();const {unmount}=render(<CameraManagementDialog view={{...view,phase:"saved",saved}} actions={a}/>);
 expect(screen.getByRole("heading",{name:"Camera saved"})).toBeTruthy();
 expect(a.watch).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"Watch now"}));expect(a.watch).toHaveBeenCalledWith("new");
 fireEvent.click(screen.getByRole("button",{name:"Add another"}));expect(a.reset).toHaveBeenCalledOnce();unmount();
 const confirm=vi.fn(),check=vi.fn(),close=vi.fn();
 const recovery=(unresolved:boolean)=><CameraRecovery name="Room 1" busy={false} unresolved={unresolved} disabled={false} onConfirm={confirm} onCheck={check} onClose={close}/>;
 // It opens as it is shown and says what stops before anything happens.
 const {rerender}=render(recovery(false));await waitFor(()=>expect(screen.getByRole("dialog",{name:"Reconnect Room 1?"})).toBeTruthy());
 expect(confirm).not.toHaveBeenCalled();expect(screen.getByText(/This stops all viewers/)).toBeTruthy();
 fireEvent.click(screen.getByRole("button",{name:"Reconnect camera"}));expect(confirm).toHaveBeenCalledOnce();
 // A reset whose outcome is unknown is checked, never started again.
 rerender(recovery(true));fireEvent.click(screen.getByRole("button",{name:"Check again"}));
 expect(check).toHaveBeenCalledOnce();expect(confirm).toHaveBeenCalledOnce();
 fireEvent.click(screen.getByRole("button",{name:"Close"}));expect(close).toHaveBeenCalledOnce();
});
const room1={id:"room1",name:"Room 1",availability:"playing" as const,can_update_credentials:true,streams:[{id:"default",name:"Default",capabilities:{video:true,audio:true}},{id:"compact",name:"Compact",capabilities:{video:true,audio:false}}]};
const target={id:"room1",name:"Room 1"};
// A sign-in draft keeps the saved address, so it names none.
const update={...draft,candidate_id:"room1",name:"Room 1",endpoint:"",credential_update:true};
const roomSettings:CameraSettings={camera_id:"room1",name:"Room 1",endpoint:"rtsp://room1.local:554/live",dial_address:"192.0.2.10:554",authentication:"credentials",catalog_revision:{runtime_epoch:"epoch",generation:1}};
const watchers=()=>document.getElementById("settings-watchers")!.textContent;
const checkAndSave=()=>screen.getByRole("button",{name:"Check and save"}) as HTMLButtonElement;
const section=(name:string)=>within(screen.getByRole("region",{name}));
const connection=()=>section("Connection").getAllByRole("button").map(b=>b.textContent);
const facts=()=>[...document.querySelectorAll(".drawer-facts div")].map(d=>d.textContent);
const described=(control:Element)=>(control.getAttribute("aria-describedby")??"").split(" ").filter(Boolean).map(id=>document.getElementById(id)?.textContent);
it("opens a saved camera's settings in a drawer that says who must stop watching", async () => {
 const user=userEvent.setup();const a={...actions(),checkAndSave:vi.fn()};const stop=vi.fn(),reconnect=vi.fn();
 const drawer=(settings:CameraSettingsContext)=><CameraManagementDialog view={{...view,credentialTarget:target}} actions={a} settings={settings}/>;
 const {rerender}=render(drawer({camera:room1,watching:true,stopWatching:stop}));
 expect(screen.getByRole("dialog",{name:"Settings Room 1"}).classList.contains("drawer")).toBe(true);
 expect(screen.queryByRole("switch")).toBeNull();expect(document.querySelector(".stepper")).toBeNull();
 // Until the server counts, the drawer states the rule.
 expect(watchers()).toBe("A new address or sign-in can be saved only while no one is watching this camera, in any browser.");
 // A count taken before this page connected still includes it.
 rerender(drawer({camera:room1,viewers:0,watching:true,stopWatching:stop}));
 expect(watchers()).toBe("You’re watching this camera. Stop watching to save a new address or sign-in.");expect(checkAndSave().disabled).toBe(true);
 rerender(drawer({camera:room1,viewers:3,watching:true,stopWatching:stop}));
 expect(watchers()).toBe("3 people are watching this camera, including you. A new address or sign-in can be saved once no one is, or after you stop everyone’s playback.");
 expect(checkAndSave().getAttribute("aria-describedby")).toBe("settings-watchers");
 fireEvent.click(screen.getByRole("button",{name:"Stop watching here"}));expect(stop).toHaveBeenCalledOnce();
 // The button goes with the playback, so focus moves on to the form.
 expect(document.activeElement).toBe(screen.getByLabelText("Username"));
 rerender(drawer({camera:room1,viewers:1}));
 expect(watchers()).toBe("1 person is watching this camera. A new address or sign-in can be saved once no one is, or after you stop everyone’s playback.");
 expect(screen.queryByRole("button",{name:"Stop watching here"})).toBeNull();expect(checkAndSave().disabled).toBe(true);
 rerender(drawer({camera:room1,viewers:0,reconnect}));
 expect(watchers()).toBe("No one is watching this camera, so a new address or sign-in can be saved now.");expect(checkAndSave().disabled).toBe(false);
 // Connection and About describe the camera; Reconnect shows only when its source needs it.
 expect(facts()).toEqual(["StatusLive","StreamsDefault, Compact","MediaVideo + audio, Video only"]);
 fireEvent.click(screen.getByRole("button",{name:"Reconnect"}));expect(reconnect).toHaveBeenCalledOnce();
 // One press checks and saves what was typed, and clears the fields.
 expect(screen.getByLabelText("New password").hasAttribute("required")).toBe(true);
 await user.type(screen.getByLabelText("New password"),"PRIVATE-PASSWORD");await user.click(checkAndSave());
 expect(a.checkAndSave).toHaveBeenCalledOnce();expect(a.checkAndSave).toHaveBeenCalledWith(expect.any(HTMLVideoElement),{username:"",password:"PRIVATE-PASSWORD"},"",false);
 expect((screen.getByLabelText("New password") as HTMLInputElement).value).toBe("");
 fireEvent.click(screen.getByRole("button",{name:"Close settings"}));expect(a.close).toHaveBeenCalledOnce();
});
it("shows the drawer's check, a refused change, an unknown outcome and the saved sign-in", () => {
 const a={...actions(),checkAndSave:vi.fn(),abandon:vi.fn(),retrySave:vi.fn()};
 const drawer=(v:ManagementView)=><CameraManagementDialog view={{...view,credentialTarget:target,...v}} actions={a} settings={{camera:room1,viewers:0}}/>;
 const {rerender}=render(drawer({...view,phase:"testing",draft:{...update,state:"testing"},test:running}));
 expect(checklist()).toEqual(["Reaching the camera: checking","Receiving video: waiting","Playing in this browser: waiting"]);
 expect(screen.getByText("Checking the connection…")).toBeTruthy();expect(connection()).toEqual(["Checking…","Stop check"]);expect(footer()).toEqual([]);
 expect((screen.getByRole("button",{name:"Checking…"}) as HTMLButtonElement).disabled).toBe(true);
 rerender(drawer({...view,phase:"testing",draft:{...update,state:"testing"},test:{...running,stage:"browser_playback",source_media_observed:true}}));
 expect(connection()).toEqual(["Checking…","Stop check","Play preview"]);
 fireEvent.click(screen.getByRole("button",{name:"Stop check"}));expect(a.cancelTest).toHaveBeenCalledOnce();
 // The server refuses while anyone watches; the password entered before can be checked again.
 rerender(drawer({...view,phase:"review",draft:{...update,state:"ready"},error:"camera_busy"}));
 expect(screen.getByRole("alert").textContent).toBe("Someone is watching this camera, or another change to it is running. Wait until it’s free, or stop everyone’s playback.");
 const password=screen.getByLabelText("New password");expect(password.hasAttribute("required")).toBe(false);
 expect(document.getElementById(password.getAttribute("aria-describedby")!)?.textContent).toBe("Leave blank to check the password you entered again.");
 rerender(drawer({...view,phase:"recovering",draft:update,unresolved:true,error:"save_outcome_unknown",canRetrySave:true,canAbandon:true}));
 expect(screen.queryByLabelText("New password")).toBeNull();expect(footer()).toEqual(["Check again","Try saving again","Discard changes"]);
 rerender(drawer({...view,credentialTarget:undefined,phase:"saved",draft:{...update,state:"tested"},saved:{...saved,camera_id:"room1",credentials_updated:true}}));
 expect(screen.getByRole("status").textContent).toBe("The camera now uses the new sign-in.");expect(footer()).toEqual(["Watch now","Close"]);
 // Focus moves on to the next step.
 expect(document.activeElement).toBe(screen.getByRole("button",{name:"Watch now"}));
 // Restored without its draft, the saved change is named generally.
 rerender(drawer({...view,credentialTarget:undefined,phase:"saved",saved:{...saved,camera_id:"room1",credentials_updated:true}}));
 expect(screen.getByRole("status").textContent).toBe("The camera now uses the new settings.");
 fireEvent.click(screen.getByRole("button",{name:"Watch now"}));expect(a.watch).toHaveBeenCalledWith("room1");
});
// Each button takes a role class from styles.css, so none falls back to the
// browser's own look.
const ROLE = /(?:^|\s)(?:(?:primary|secondary|ghost|danger|icon|control)-button|add-camera|overlay-cameras|menu-item)(?:\s|$)/;
const unstyled = () => [...document.querySelectorAll("button")].filter(b => !ROLE.test(b.className)).map(b => b.textContent || b.getAttribute("aria-label"));
it("draws every management, recovery, rail and menu button in a role", async () => {
 const user=userEvent.setup();
 const views:ManagementView[]=[view,{...view,phase:"review",draft},{...view,phase:"testing",draft:{...draft,state:"testing"},test:{...running,source_media_observed:true}},
  {...view,phase:"review",draft,test:failedTest("camera_unreachable")},{...view,phase:"review",draft:tested,test:tested.test},
  {...view,phase:"recovering",draft,unresolved:true,error:"save_outcome_unknown",canRetrySave:true,canAbandon:true},{...view,phase:"saved",saved},{...view,phase:"canceled"},
  {...view,phase:"review",draft,error:"duplicate_camera",duplicateCamera:"room1"},
  {...view,credentialTarget:target},{...view,credentialTarget:target,phase:"testing",draft:{...update,state:"testing"},test:{...running,source_media_observed:true}},
  {...view,credentialTarget:target,phase:"recovering",draft:update,unresolved:true,error:"save_outcome_unknown",canRetrySave:true,canAbandon:true},{...view,phase:"saved",saved:{...saved,credentials_updated:true}},
  {...view,credentialTarget:target,settings:roomSettings},{...view,credentialTarget:target,settings:roomSettings,change:{camera_id:"room1",catalog_revision:roomSettings.catalog_revision,removed:true}}];
 const settings:CameraSettingsContext={camera:room1,viewers:2,watching:true,stopWatching:vi.fn(),reconnect:vi.fn()};
 for(const v of views){const {unmount}=render(<CameraManagementDialog view={v} actions={{...actions(),openSettings:vi.fn(),abandon:vi.fn(),retrySave:vi.fn()}} settings={settings}/>);expect(unstyled(),v.phase).toEqual([]);unmount();}
 // The confirmations' buttons too.
 for(const open of ["Remove camera…","Stop everyone’s playback…"]){
  const {unmount}=render(<CameraManagementDialog view={{...view,credentialTarget:target,settings:roomSettings}} actions={actions()} settings={settings}/>);
  await user.click(screen.getByRole("button",{name:open}));expect(screen.getByRole("group",{name:/\?/})).toBeTruthy();expect(unstyled(),open).toEqual([]);unmount();
 }
 render(<CameraManagementDialog view={view} actions={actions()}/>);
 await user.click(screen.getByRole("radio",{name:"Camstream Mac app"}));expect(unstyled()).toEqual([]);cleanup();
 render(<App catalog={{cameras:[]}} onAddCamera={vi.fn()}/>);expect(unstyled()).toEqual([]);cleanup();
 const cameras=[{...room1,availability:"idle" as const,streams:[{id:"default",name:"Default",capabilities:{video:true,audio:false},can_reconnect:true,recovery_required:true}]}];
 render(<App catalog={{cameras}} catalogStale refreshCatalog={vi.fn(async()=>{})} onAddCamera={vi.fn()} onCameraSettings={vi.fn()} onReconnect={vi.fn()}/>);
 expect(within(screen.getByRole("region",{name:"Camera details"})).getAllByRole("button").map(b=>b.textContent)).toEqual(["Reconnect","Settings"]);
 await user.click(screen.getByRole("button",{name:"Room 1 actions"}));
 expect(screen.getAllByRole("menuitem").map(item=>item.textContent)).toEqual(["Settings","Reconnect","Copy link"]);
 expect(unstyled()).toEqual([]);cleanup();
 render(<CameraRecovery name="Room 1" busy={false} unresolved={false} disabled={false} onConfirm={vi.fn()} onCheck={vi.fn()} onClose={vi.fn()}/>);
 expect(unstyled()).toEqual([]);
});
it("closes from its icon and offers Cancel only with a draft and Close only at the end", () => {
 const a=actions();const {rerender}=render(<CameraManagementDialog view={view} actions={a}/>);
 expect(screen.getByRole("button",{name:"Close Add Camera"}).querySelector("svg")).toBeTruthy();expect(footer()).toEqual(["Continue"]);
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft}} actions={a}/>);expect(footer()).toEqual(["Check connection","Edit details","Cancel"]);
 fireEvent.click(screen.getByRole("button",{name:"Cancel"}));expect(a.cancel).toHaveBeenCalledOnce();
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft:tested,test:tested.test}} actions={a}/>);expect(footer()).toEqual(["Save camera","Edit details","Cancel"]);
 rerender(<CameraManagementDialog view={{...view,phase:"recovering",draft,unresolved:true,error:"save_outcome_unknown",canAbandon:true}} actions={a}/>);expect(footer()).toEqual(["Check again","Discard changes"]);
 rerender(<CameraManagementDialog view={{...view,phase:"canceled"}} actions={a}/>);expect(footer()).toEqual(["Close"]);
 rerender(<CameraManagementDialog view={{...view,phase:"saved",saved}} actions={a}/>);expect(footer()).toEqual(["Watch now","Add another","Close"]);
 fireEvent.click(screen.getByRole("button",{name:"Close Add Camera"}));expect(a.close).toHaveBeenCalledOnce();
});
it("links each hint and file error to its control", async () => {
 const user=userEvent.setup();const {unmount}=render(<CameraManagementDialog view={view} actions={actions()}/>);
 const notes=(control:Element)=>(control.getAttribute("aria-describedby")??"").split(" ").filter(Boolean).map(id=>document.getElementById(id)?.textContent);
 const address="An address the server can reach, without a username or password.";
 expect(notes(screen.getByLabelText("RTSP address"))).toEqual([address]);
 await user.type(screen.getByLabelText("RTSP address"),draft.endpoint);
 expect(notes(screen.getByLabelText("RTSP address"))).toEqual([address,expect.stringMatching(/^Localhost is the server’s own machine/)]);
 expect(notes(screen.getByLabelText("Password"))).toEqual(["For the Camstream Mac app, both are in its private .env file."]);
 const toggle=screen.getByRole("switch",{name:"Requires sign-in"});expect(notes(toggle)).toEqual([]);
 await user.click(toggle);expect(notes(toggle)).toEqual(["Leave this on for the Camstream Mac app, which always requires sign-in."]);
 await user.click(screen.getByRole("radio",{name:"Camstream Mac app"}));
 const choose=screen.getByRole("button",{name:"Choose files"});expect(notes(choose)).toEqual(["Sharing settings in the file are not imported."]);
 fireEvent.change(picker(),{target:{files:[new File(["x".repeat(9000)],"large.env")]}});
 expect(notes(choose)).toEqual(["Sharing settings in the file are not imported.","large.env is too large for a Camstream export."]);unmount();
 render(<CameraManagementDialog view={{...view,phase:"review",draft}} actions={actions()}/>);
 await user.click(screen.getByRole("button",{name:"Edit details"}));
 for(const label of ["Username","Password"])expect(notes(screen.getByLabelText(label))).toEqual(["Leave both blank to keep the ones already entered."]);cleanup();
 render(<CameraManagementDialog view={{...view,credentialTarget:{id:"room1",name:"Room 1"},settings:roomSettings}} actions={actions()} settings={{camera:room1}}/>);
 expect(notes(screen.getByLabelText("Username"))).toEqual(["Leave blank to keep the saved username."]);
 expect(notes(screen.getByLabelText("Address"))).toEqual([address]);
});
it("renames a camera from its settings, and shows another manager's camera read-only", async () => {
 const user=userEvent.setup();const a={...actions(),rename:vi.fn()};
 const drawer=(v:Partial<ManagementView>,camera=room1)=><CameraManagementDialog view={{...view,credentialTarget:target,...v}} actions={a} settings={{camera,viewers:0}}/>;
 const {rerender}=render(drawer({}));
 const field=()=>screen.getByRole("textbox",{name:"Name"}) as HTMLInputElement;
 const said=()=>section("Name").getByRole("status").textContent;
 // Until its settings are read, the name waits.
 expect(field().disabled).toBe(true);expect((screen.getByRole("button",{name:"Save name"}) as HTMLButtonElement).disabled).toBe(true);
 rerender(drawer({settings:roomSettings}));
 expect(field().disabled).toBe(false);expect(field().value).toBe("Room 1");expect(facts()).toContain("Connects to192.0.2.10:554");
 await user.clear(field());await user.type(field(),"Porch{Enter}");
 expect(a.rename).toHaveBeenCalledExactlyOnceWith("Porch");
 rerender(drawer({settings:roomSettings,busy:true,unresolved:true}));
 expect(said()).toBe("Saving the name…");expect(field().disabled).toBe(true);
 rerender(drawer({settings:roomSettings,change:{camera_id:"room1",catalog_revision:{runtime_epoch:"epoch",generation:2}}}));
 expect(said()).toBe("Name saved.");
 // A refusal shows beside the name.
 rerender(drawer({settings:roomSettings,error:"catalog_revision_conflict"}));
 expect(section("Name").getByRole("alert").textContent).toBe("The camera list changed. Review the details and save again.");
 expect(section("Connection").queryByRole("alert")).toBeNull();
 // Another manager's camera, or a server camera, shows its details only.
 rerender(drawer({},{...room1,can_update_credentials:false}));
 expect(screen.getByText("Only the server’s configuration, or the manager who added this camera, can change it.")).toBeTruthy();
 expect(screen.queryByRole("textbox",{name:"Name"})).toBeNull();for(const label of ["Address","New password"])expect(screen.queryByLabelText(label)).toBeNull();
 expect(screen.queryByRole("button",{name:"Remove camera…"})).toBeNull();expect(facts()).toEqual(["StatusLive","StreamsDefault, Compact","MediaVideo + audio, Video only"]);
});
it("changes the address beside the sign-in, and sends it only when it changed", async () => {
 const user=userEvent.setup();const a={...actions(),checkAndSave:vi.fn()};const moved="rtsp://localhost:18556/moved";
 const drawer=(v:Partial<ManagementView>={},s=roomSettings)=><CameraManagementDialog view={{...view,credentialTarget:target,settings:s,...v}} actions={a} settings={{camera:room1,viewers:0}}/>;
 const {rerender}=render(drawer());
 const address=screen.getByLabelText("Address") as HTMLInputElement,password=screen.getByLabelText("New password") as HTMLInputElement;
 expect(address.value).toBe(roomSettings.endpoint);
 // The saved address alone changes nothing, so a new password is needed.
 expect(password.required).toBe(true);expect(described(password)).toEqual([]);
 await user.type(password,"PRIVATE-PASSWORD");await user.click(checkAndSave());
 expect(a.checkAndSave).toHaveBeenLastCalledWith(expect.any(HTMLVideoElement),{username:"",password:"PRIVATE-PASSWORD"},"",false);
 // A new address keeps the saved sign-in unless one is entered.
 await user.clear(address);await user.type(address,` ${moved} `);
 expect(password.required).toBe(false);expect(described(password)).toEqual(["Leave blank to keep the saved password."]);
 expect(described(address)).toEqual(["An address the server can reach, without a username or password.",expect.stringMatching(/^Localhost is the server’s own machine/)]);
 await user.click(checkAndSave());
 expect(a.checkAndSave).toHaveBeenLastCalledWith(expect.any(HTMLVideoElement),undefined,moved,false);
 // The address stays as typed; a draft for it is checked again as is.
 expect(address.value).toBe(` ${moved} `);
 rerender(drawer({phase:"review",draft:{...update,endpoint:moved,state:"ready"},error:"authentication_failed"}));
 expect(described(password)).toEqual(["Leave blank to check the password you entered again."]);
 expect(section("Connection").getByRole("alert").textContent).toBe("The camera rejected the username or password. Check them and try again.");
 rerender(drawer({phase:"saved",draft:{...update,endpoint:moved,state:"tested"},saved:{...saved,camera_id:"room1",credentials_updated:true}}));
 expect(screen.getByRole("status").textContent).toBe("The camera now uses the new address.");
 cleanup();
 // An anonymous camera keeps connecting without a sign-in.
 render(drawer({},{...roomSettings,authentication:"anonymous"}));
 expect(described(screen.getByLabelText("Username"))).toEqual(["This camera connects without a sign-in. Leave both blank to keep it that way."]);
});
it("removes a camera after a confirmation that names it and who watches", async () => {
 const user=userEvent.setup();const a={...actions(),remove:vi.fn()};
 const drawer=(v:Partial<ManagementView>,viewers=0,watching=false)=><CameraManagementDialog view={{...view,credentialTarget:target,settings:roomSettings,...v}} actions={a} settings={{camera:room1,viewers,watching}}/>;
 const {rerender}=render(drawer({}));
 const ask=()=>screen.getByRole("group",{name:/^Remove Room 1\?/});
 await user.click(screen.getByRole("button",{name:"Remove camera…"}));
 expect(ask().textContent).toContain("Remove Room 1? It leaves every camera list, and its saved address and sign-in are deleted.");
 expect(document.activeElement).toBe(within(ask()).getByRole("button",{name:"Remove camera"}));
 // Cancel gives focus back to the button that asked.
 await user.click(within(ask()).getByRole("button",{name:"Cancel"}));
 expect(screen.queryByRole("group",{name:/\?/})).toBeNull();expect(document.activeElement).toBe(screen.getByRole("button",{name:"Remove camera…"}));
 // While anyone watches, it says so and stops that playback.
 rerender(drawer({},2,true));
 await user.click(screen.getByRole("button",{name:"Remove camera…"}));
 expect(ask().textContent).toContain("2 people are watching it, including you, and removing it stops that playback.");
 // Escape closes the confirmation, not the drawer.
 fireEvent(screen.getByRole("dialog"),new Event("cancel",{cancelable:true}));
 expect(screen.queryByRole("group",{name:/\?/})).toBeNull();expect(a.close).not.toHaveBeenCalled();
 await user.click(screen.getByRole("button",{name:"Remove camera…"}));
 await user.click(screen.getByRole("button",{name:"Stop playback and remove"}));
 expect(a.remove).toHaveBeenCalledExactlyOnceWith(true);
 rerender(drawer({busy:true,unresolved:true},2,true));
 expect(section("Remove camera").getByRole("status").textContent).toBe("Removing the camera…");
 rerender(drawer({error:"camera_busy"},2,true));
 expect(section("Remove camera").getByRole("alert").textContent).toMatch(/^Someone is watching this camera/);
 rerender(drawer({change:{camera_id:"room1",catalog_revision:{runtime_epoch:"epoch",generation:2},removed:true}}));
 expect(screen.getByRole("status").textContent).toBe("The camera is removed, with its saved address and sign-in.");
 expect(footer()).toEqual(["Close"]);expect(document.activeElement).toBe(screen.getByRole("button",{name:"Close"}));
 expect(screen.queryByRole("textbox",{name:"Name"})).toBeNull();
});
it("offers to stop everyone's playback with the count, then checks and saves", async () => {
 const user=userEvent.setup();const a={...actions(),checkAndSave:vi.fn()};
 const drawer=(v:Partial<ManagementView>,viewers:number)=><CameraManagementDialog view={{...view,credentialTarget:target,settings:roomSettings,...v}} actions={a} settings={{camera:room1,viewers,watching:true,stopWatching:vi.fn()}}/>;
 const {rerender}=render(drawer({},1));
 // Only this page watches, so stopping here is enough.
 expect(screen.queryByRole("button",{name:"Stop everyone’s playback…"})).toBeNull();
 rerender(drawer({},3));
 expect(checkAndSave().disabled).toBe(true);
 await user.click(screen.getByRole("button",{name:"Stop everyone’s playback…"}));
 const ask=screen.getByRole("group",{name:/^Stop playback for 3 people/});
 expect(ask.textContent).toContain("Stop playback for 3 people, including you? Their video stops in every browser and can start again once this change is saved or canceled.");
 // The form still needs a change before anyone is stopped.
 await user.click(within(ask).getByRole("button",{name:"Stop playback and save"}));
 expect(a.checkAndSave).not.toHaveBeenCalled();
 await user.type(screen.getByLabelText("New password"),"PRIVATE-PASSWORD");
 await user.click(screen.getByRole("button",{name:"Stop playback and save"}));
 expect(a.checkAndSave).toHaveBeenCalledExactlyOnceWith(expect.any(HTMLVideoElement),{username:"",password:"PRIVATE-PASSWORD"},"",true);
 expect(screen.queryByRole("group",{name:/\?/})).toBeNull();
 // While the change holds the fence, the count no longer blocks a check.
 rerender(drawer({phase:"review",draft:{...update,fenced:true}},3));
 expect(watchers()).toBe("Playback is stopped in every browser until this change is saved or canceled.");
 expect(checkAndSave().disabled).toBe(false);expect(screen.queryByRole("button",{name:/^Stop/})).toBeNull();
});
it("shows a restored draft's new address, so checking again keeps that draft", async () => {
 const user=userEvent.setup();const a={...actions(),checkAndSave:vi.fn()};const moved="rtsp://192.0.2.40:554/live";
 render(<CameraManagementDialog view={{...view,credentialTarget:target,settings:roomSettings,phase:"review",draft:{...update,endpoint:moved}}} actions={a} settings={{camera:room1,viewers:0}}/>);
 expect((screen.getByLabelText("Address") as HTMLInputElement).value).toBe(moved);
 expect(described(screen.getByLabelText("New password"))).toEqual(["Leave blank to check the password you entered again."]);
 await user.click(checkAndSave());
 expect(a.checkAndSave).toHaveBeenCalledExactlyOnceWith(expect.any(HTMLVideoElement),undefined,moved,false);
});
