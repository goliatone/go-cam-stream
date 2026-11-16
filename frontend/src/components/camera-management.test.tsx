import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import CameraManagementDialog, { type ManagementActions, type ManagementView } from "./camera-management";
import { CameraRecovery } from "./camera-recovery";
import { App } from "../app";
import type { DraftReview } from "../services/contracts";

afterEach(cleanup);
const actions = (): ManagementActions => ({submit:vi.fn(),importFiles:vi.fn(),test:vi.fn(),cancelTest:vi.fn(),save:vi.fn(),recover:vi.fn(),cancel:vi.fn(),close:vi.fn(),watch:vi.fn()});
const draft: DraftReview = {id:"draft",candidate_id:"camera",revision:1,runtime_epoch:"epoch",expires_at:"2030-01-01T00:00:00Z",name:"Mac",endpoint:"rtsp://localhost:18556/macbook",authentication:"credentials",credentials_present:true,warnings:["server_relative_localhost","imported_grants_ignored","creator_only_access"],state:"ready"};
const view: ManagementView = {phase:"editing",storageReady:true};
it("offers manager entry on an empty catalog without showing it to viewers", () => {
 const {rerender}=render(<App catalog={{cameras:[]}} />);
 expect(screen.queryByRole("button",{name:"Add Camera"})).toBeNull();
 const add=vi.fn();rerender(<App catalog={{cameras:[]}} onAddCamera={add}/>);
 fireEvent.click(screen.getByRole("button",{name:"Add Camera"}));expect(add).toHaveBeenCalledOnce();
});
it("transfers manual credentials once and clears the input fields", async () => {
 const user=userEvent.setup();const a=actions();render(<CameraManagementDialog view={view} actions={a}/>);
 await user.type(screen.getByLabelText("Camera name"),"Mac");await user.type(screen.getByLabelText("RTSP address"),draft.endpoint);
 await user.selectOptions(screen.getByLabelText("Authentication"),"credentials");
 await user.type(screen.getByLabelText("Username"),"camera-user");await user.type(screen.getByLabelText("Password"),"PRIVATE-PASSWORD");
 await user.click(screen.getByRole("button",{name:"Review camera"}));
 expect(a.submit).toHaveBeenCalledWith({name:"Mac",endpoint:draft.endpoint,authentication:"credentials",credentials:{username:"camera-user",password:"PRIVATE-PASSWORD"}});
 expect((screen.getByLabelText("Password") as HTMLInputElement).value).toBe("");
 expect((screen.getByLabelText("Username") as HTMLInputElement).value).toBe("");
});
it("reviews hidden credentials, warns about localhost/grants and requires a cleaned pass", () => {
 const a=actions();const {rerender}=render(<CameraManagementDialog view={{...view,phase:"review",draft}} actions={a}/>);
 expect(screen.getByText(/Localhost is the server/)).toBeTruthy();expect(screen.getByText(/Access settings in the file are ignored/)).toBeTruthy();
 expect(screen.queryByLabelText("Password")).toBeNull();expect((screen.getByRole("button",{name:"Save Camera"}) as HTMLButtonElement).disabled).toBe(true);
 const tested={...draft,state:"tested" as const,test:{id:"test",attempt_key:"attempt",draft_revision:1,stage:"passed" as const,expires_at:draft.expires_at,source_media_observed:true,browser_frames_reported:true,cleanup:"confirmed" as const}};
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft:tested,test:tested.test}} actions={a}/>);
 fireEvent.click(screen.getByRole("button",{name:"Save Camera"}));expect(a.save).toHaveBeenCalledOnce();
});
it("does not claim anonymous credentials are unnecessary and explains source failures", () => {
 const anonymous={...draft,authentication:"anonymous" as const,credentials_present:false};
 const failed={id:"test",attempt_key:"attempt",draft_revision:1,stage:"failed" as const,expires_at:draft.expires_at,source_media_observed:false,browser_frames_reported:false,cleanup:"confirmed" as const,error:{code:"source_unavailable" as const,message:"untrusted backend detail",retryable:true}};
 const {rerender}=render(<CameraManagementDialog view={{...view,phase:"review",draft:anonymous,test:failed}} actions={actions()}/>);
 expect(screen.queryByText("Not required")).toBeNull();
 expect(screen.getByText("Not provided (anonymous access)")).toBeTruthy();
 expect(screen.getByText(/Camstream Camera on Mac always requires credentials/)).toBeTruthy();
 expect(screen.getByRole("alert").textContent).toMatch(/credentials are provided and correct/);
 expect(screen.queryByText(/untrusted backend detail/)).toBeNull();
 expect((screen.getByRole("button",{name:"Save Camera"}) as HTMLButtonElement).disabled).toBe(true);
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:{...failed,error:{...failed.error,code:"authentication_failed"}}}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toMatch(/camera rejected its credentials/);
});
it("distinguishes preview timeouts from an expired successful test", () => {
 const failed={id:"test",attempt_key:"attempt",draft_revision:1,stage:"failed" as const,expires_at:draft.expires_at,source_media_observed:true,browser_frames_reported:false,cleanup:"confirmed" as const,error:{code:"test_expired" as const,message:"untrusted timeout detail",retryable:true}};
 const {rerender}=render(<CameraManagementDialog view={{...view,phase:"review",draft,test:failed}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toMatch(/Camera video reached the server/);
 expect(screen.getByRole("alert").textContent).toContain("--media-address");
 expect((screen.getByRole("button",{name:"Save Camera"}) as HTMLButtonElement).disabled).toBe(true);
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:{...failed,source_media_observed:false}}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toMatch(/before camera video was verified/);
 expect(screen.getByRole("alert").textContent).not.toContain("Firefox");
 rerender(<CameraManagementDialog view={{...view,phase:"review",draft,test:{...failed,browser_frames_reported:true}}} actions={actions()}/>);
 expect(screen.getByRole("alert").textContent).toBe("Test this camera again before saving.");
 expect(screen.queryByText(/untrusted timeout detail/)).toBeNull();
});
it("disables mutations when storage or an operation outcome is unresolved", () => {
 const a=actions();const {rerender}=render(<CameraManagementDialog view={{...view,storageReady:false}} actions={a}/>);
 expect(screen.getByRole("alert").textContent).toMatch(/Enable site storage/);expect((screen.getByRole("button",{name:"Review camera"}) as HTMLButtonElement).closest("fieldset")?.disabled).toBe(true);
 rerender(<CameraManagementDialog view={{...view,phase:"recovering",draft,unresolved:true,error:"save_outcome_unknown"}} actions={a}/>);
 fireEvent.click(screen.getByRole("button",{name:"Check status"}));expect(a.recover).toHaveBeenCalledOnce();expect((screen.getByRole("button",{name:"Save Camera"}) as HTMLButtonElement).disabled).toBe(true);
});
it("clears file inputs after import and explains missing credentials", async () => {
 const user=userEvent.setup();const a=actions();render(<CameraManagementDialog view={view} actions={a}/>);
 await user.click(screen.getByRole("button",{name:"Import Mac files"}));
 const file=new File(["camera data"],"camstream-camera.yaml",{type:"text/yaml"});
 await user.upload(screen.getByLabelText("Connection YAML"),file);
 expect((screen.getByLabelText("Connection YAML") as HTMLInputElement).files?.length).toBe(1);
 fireEvent.submit(screen.getByRole("button",{name:"Import and review"}).closest("form")!);
 expect(a.importFiles).toHaveBeenCalledWith(file,undefined);expect((screen.getByLabelText("Connection YAML") as HTMLInputElement).files?.length).toBe(0);
});
it("Watch is explicit and source reconnect requires impact confirmation", async () => {
 const a=actions();const {unmount}=render(<CameraManagementDialog view={{...view,phase:"saved",saved:{camera_id:"new",catalog_revision:{runtime_epoch:"epoch",generation:2}}}} actions={a}/>);
 expect(a.watch).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"Watch Camera"}));expect(a.watch).toHaveBeenCalledWith("new");unmount();
 const confirm=vi.fn();render(<CameraRecovery busy={false} unresolved={false} disabled={false} onConfirm={confirm} onCheck={vi.fn()}/>);
 fireEvent.click(screen.getByRole("button",{name:"Reconnect Camera"}));await waitFor(()=>expect(screen.getByRole("dialog")).toBeTruthy());
 expect(confirm).not.toHaveBeenCalled();expect(screen.getByText(/This stops all viewers/)).toBeTruthy();
 fireEvent.click(screen.getAllByRole("button",{name:"Reconnect Camera"})[1]!);expect(confirm).toHaveBeenCalledOnce();
});
