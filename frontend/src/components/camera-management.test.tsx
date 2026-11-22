import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import CameraManagementDialog, { type ManagementActions, type ManagementView } from "./camera-management";
import { CameraRecovery } from "./camera-recovery";
import { App } from "../app";
import type { DraftReview, ManagementErrorCode, TestResult } from "../services/contracts";

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
 const confirm=vi.fn();render(<CameraRecovery busy={false} unresolved={false} disabled={false} onConfirm={confirm} onCheck={vi.fn()}/>);
 fireEvent.click(screen.getByRole("button",{name:"Reconnect Camera"}));await waitFor(()=>expect(screen.getByRole("dialog")).toBeTruthy());
 expect(confirm).not.toHaveBeenCalled();expect(screen.getByText(/This stops all viewers/)).toBeTruthy();
 fireEvent.click(screen.getAllByRole("button",{name:"Reconnect Camera"})[1]!);expect(confirm).toHaveBeenCalledOnce();
});
it("updates a saved camera's sign-in through the same steps", () => {
 const target={id:"room1",name:"Room 1"};const update={...tested,credential_update:true,name:"Room 1"};
 const {rerender}=render(<CameraManagementDialog view={{...view,credentialTarget:target}} actions={actions()}/>);
 expect(screen.getByRole("heading",{name:"Camera settings"})).toBeTruthy();expect(steps()).toEqual(["Sign-in (current)","Check","Save"]);
 expect(screen.queryByRole("switch")).toBeNull();expect(screen.getByLabelText("New password")).toBeTruthy();
 rerender(<CameraManagementDialog view={{...view,credentialTarget:target,phase:"review",draft:update,test:update.test}} actions={actions()}/>);
 expect(footer()).toEqual(["Save sign-in","Edit sign-in","Cancel"]);
 expect([...document.querySelectorAll(".management-review dt")].map(dt=>dt.textContent)).toEqual(["Sign-in"]);
 rerender(<CameraManagementDialog view={{...view,credentialTarget:target,phase:"review",draft:{...update,state:"ready"},error:"camera_busy"}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toBe("Stop playback of this camera, then try again.");
 rerender(<CameraManagementDialog view={{...view,phase:"saved",saved:{...saved,credentials_updated:true}}} actions={actions()}/>);
 expect(screen.getByRole("heading",{name:"Sign-in updated"})).toBeTruthy();expect(footer()).toEqual(["Watch now","Close"]);
});
// Each button takes a role class from styles.css, so none falls back to the
// browser's own look.
const ROLE = /(?:^|\s)(?:(?:primary|secondary|ghost|danger|icon|control)-button|add-camera|overlay-cameras)(?:\s|$)/;
const unstyled = () => [...document.querySelectorAll("button")].filter(b => !ROLE.test(b.className)).map(b => b.textContent || b.getAttribute("aria-label"));
it("draws every management, recovery and rail button in a role", async () => {
 const user=userEvent.setup();
 const views:ManagementView[]=[view,{...view,phase:"review",draft},{...view,phase:"testing",draft:{...draft,state:"testing"},test:{...running,source_media_observed:true}},
  {...view,phase:"review",draft,test:failedTest("camera_unreachable")},{...view,phase:"review",draft:tested,test:tested.test},
  {...view,phase:"recovering",draft,unresolved:true,error:"save_outcome_unknown",canRetrySave:true,canAbandon:true},{...view,phase:"saved",saved},{...view,phase:"canceled"},
  {...view,credentialTarget:{id:"room1",name:"Room 1"},error:"duplicate_camera",duplicateCamera:"room1"}];
 for(const v of views){const {unmount}=render(<CameraManagementDialog view={v} actions={{...actions(),openSettings:vi.fn(),abandon:vi.fn(),retrySave:vi.fn()}}/>);expect(unstyled(),v.phase).toEqual([]);unmount();}
 render(<CameraManagementDialog view={view} actions={actions()}/>);
 await user.click(screen.getByRole("radio",{name:"Camstream Mac app"}));expect(unstyled()).toEqual([]);cleanup();
 render(<App catalog={{cameras:[]}} onAddCamera={vi.fn()}/>);expect(unstyled()).toEqual([]);cleanup();
 const recovery=<CameraRecovery busy={false} unresolved={false} disabled={false} onConfirm={vi.fn()} onCheck={vi.fn()}/>;
 const cameras=[{id:"room1",name:"Room 1",availability:"idle" as const,can_update_credentials:true,streams:[{id:"default",name:"Default",capabilities:{video:true,audio:false}}]}];
 render(<App catalog={{cameras}} catalogStale refreshCatalog={vi.fn(async()=>{})} onAddCamera={vi.fn()} onCameraSettings={vi.fn()} recoveryControl={recovery}/>);
 fireEvent.click(screen.getByRole("button",{name:"Reconnect Camera"}));await waitFor(()=>expect(screen.getByRole("dialog",{name:"Reconnect this camera?"})).toBeTruthy());
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
 render(<CameraManagementDialog view={{...view,credentialTarget:{id:"room1",name:"Room 1"}}} actions={actions()}/>);
 expect(notes(screen.getByLabelText("Username"))).toEqual(["Leave blank to keep the saved username."]);
});
