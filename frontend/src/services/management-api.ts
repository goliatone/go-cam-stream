import type { Capabilities, CompleteTestRequest, CreateDraftRequest, CreateTestRequest, CredentialDraftRequest, DraftReview, ManagementErrorCode, Mutation, OperationKind, OperationResult, PatchDraftRequest, ReconnectResponse, SaveCameraRequest, SaveCameraResponse, StreamRef, TestResult } from "./contracts";
import { CSRF_HEADER, CSRF_PATH } from "./contracts";
import { errorCopy } from "./copy";
import { managementCopy } from "./management-copy";

export class ManagementFailure extends Error {
  constructor(readonly code: ManagementErrorCode = "server_unavailable", readonly cameraID?: string) { super(code); }
}
export const managementCode = (reason: unknown): ManagementErrorCode => reason instanceof ManagementFailure ? reason.code : "server_unavailable";
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value);
const own = (table: object, key: string) => Object.prototype.hasOwnProperty.call(table, key);
const string = (value: unknown): value is string => typeof value === "string";
const number = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) >= 0;
export const duplicateCameraID = (code: unknown, id: unknown) =>
  code === "duplicate_camera" && string(id) && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$/.test(id) ? id : undefined;
function invalid(): never { throw new ManagementFailure(); }
function capabilities(value: unknown): Capabilities {
  if (!object(value) || typeof value.can_manage_cameras !== "boolean" || !string(value.recovery_scope) || !string(value.runtime_epoch)) return invalid();
  return {can_manage_cameras:value.can_manage_cameras,recovery_scope:value.recovery_scope,runtime_epoch:value.runtime_epoch};
}
function draft(value: unknown): DraftReview {
  if (!object(value) || !string(value.id) || !string(value.candidate_id) || !number(value.revision) || !string(value.runtime_epoch) || !string(value.expires_at) || !string(value.name) || !string(value.endpoint) || !["anonymous","credentials"].includes(String(value.authentication)) || typeof value.credentials_present !== "boolean" || !Array.isArray(value.warnings) || !string(value.state)) return invalid();
  return value as unknown as DraftReview;
}
function test(value: unknown): TestResult {
  if (!object(value) || !string(value.id) || !string(value.attempt_key) || !number(value.draft_revision) || !string(value.stage) || !string(value.cleanup) || !string(value.expires_at) || typeof value.source_media_observed !== "boolean") return invalid();
  if (value.answer !== undefined && (!object(value.answer) || value.answer.type !== "answer" || !string(value.answer.sdp))) return invalid();
  return value as unknown as TestResult;
}
function operation(value: unknown): OperationResult {
  if (!object(value) || !string(value.operation_key) || !string(value.kind) || !string(value.state) || !string(value.runtime_epoch)) return invalid();
  return value as unknown as OperationResult;
}
export interface ManagementAPI {
  capabilities(): Promise<Capabilities>;
  draft(id: string): Promise<DraftReview>;
  operation(key: string, epoch: string, candidate?: string, kind?: OperationKind, draft?: string): Promise<OperationResult>;
  create(request: CreateDraftRequest): Promise<DraftReview>;
  credentialDraft(request: CredentialDraftRequest): Promise<DraftReview>;
  import(request: Mutation, yaml: File, env?: File): Promise<DraftReview>;
  patch(id: string, request: PatchDraftRequest): Promise<DraftReview>;
  cancel(id: string, request: Mutation): Promise<DraftReview>;
  test(id: string, request: CreateTestRequest): Promise<TestResult>;
  testStatus(id: string, test: string): Promise<TestResult>;
  complete(id: string, test: string, request: CompleteTestRequest): Promise<TestResult>;
  cancelTest(id: string, test: string, request: Mutation): Promise<TestResult>;
  save(request: SaveCameraRequest): Promise<SaveCameraResponse>;
  reconnect(ref: StreamRef, request: Mutation): Promise<ReconnectResponse>;
}
export class ManagementClient implements ManagementAPI {
  #token: string | undefined;
  constructor(readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
  async #request(path: string, method = "GET", body?: unknown): Promise<unknown> {
    const abort = new AbortController();
    const timer = setTimeout(() => abort.abort(), 30000);
    try {
      const headers: Record<string,string> = {};
      if (method !== "GET") {
        if (!this.#token) {
          const token = await this.#request(CSRF_PATH);
          if (!object(token) || token.header_name !== CSRF_HEADER || !string(token.token) || !token.token || token.token.length > 8192) return invalid();
          this.#token = token.token;
        }
        headers[CSRF_HEADER] = this.#token;
      }
      const multipart = body instanceof FormData;
      if (body !== undefined && !multipart) headers["Content-Type"] = "application/json";
      const result = await this.fetcher(path, {method,headers,credentials:"same-origin",redirect:"error",cache:"no-store",signal:abort.signal,...(body === undefined ? {} : {body:multipart ? body : JSON.stringify(body)})});
      const value: unknown = await result.json();
      if (!result.ok) {
        const error = object(value) && object(value.error) ? value.error : undefined;
        const code = string(error?.code) ? error.code : "server_unavailable";
        if (code === "csrf_invalid") this.#token = undefined;
        throw new ManagementFailure(own(errorCopy,code) || own(managementCopy,code) || code === "operation_conflict" ? code as ManagementErrorCode : "server_unavailable", duplicateCameraID(code, error?.camera_id));
      }
      if (result.status !== 200) return invalid();
      return value;
    } catch (error) { if (error instanceof ManagementFailure) throw error; throw new ManagementFailure(); }
    finally { clearTimeout(timer); }
  }
  #draft(id: string) { return `/api/camera-drafts/${encodeURIComponent(id)}`; }
  async capabilities() { return capabilities(await this.#request("/api/capabilities")); }
  async draft(id: string) { return draft(await this.#request(this.#draft(id))); }
  async operation(key:string,epoch:string,candidate?:string,kind?:OperationKind,draft?:string) {
    const params = new URLSearchParams({runtime_epoch:epoch});
    if (candidate) params.set("candidate_id",candidate);
    if (draft) params.set("draft_id",draft);
    if (kind) params.set("kind",kind);
    return operation(await this.#request(`/api/camera-operations/${encodeURIComponent(key)}?${params}`));
  }
  async create(request:CreateDraftRequest) { return draft(await this.#request("/api/camera-drafts","POST",request)); }
  async credentialDraft(request: CredentialDraftRequest) {
    return draft(await this.#request("/api/camera-drafts/credentials", "POST", request));
  }
  async import(request:Mutation,yaml:File,env?:File) {
    if (yaml.size > 32768 || (env && env.size > 8192)) throw new ManagementFailure("import_invalid");
    const body=new FormData();body.set("operation_key",request.operation_key);body.set("runtime_epoch",request.runtime_epoch);body.set("yaml",yaml);if(env)body.set("env",env);
    return draft(await this.#request("/api/camera-drafts/import","POST",body));
  }
  async patch(id:string,request:PatchDraftRequest) { return draft(await this.#request(this.#draft(id),"PATCH",request)); }
  async cancel(id:string,request:Mutation) { return draft(await this.#request(this.#draft(id),"DELETE",request)); }
  async test(id:string,request:CreateTestRequest) { return test(await this.#request(this.#draft(id)+"/tests","POST",request)); }
  async testStatus(id:string,testID:string) { return test(await this.#request(this.#draft(id)+"/tests/"+encodeURIComponent(testID))); }
  async complete(id:string,testID:string,request:CompleteTestRequest) { return test(await this.#request(this.#draft(id)+"/tests/"+encodeURIComponent(testID)+"/complete","POST",request)); }
  async cancelTest(id:string,testID:string,request:Mutation) { return test(await this.#request(this.#draft(id)+"/tests/"+encodeURIComponent(testID),"DELETE",request)); }
  async save(request:SaveCameraRequest) {
    const value=await this.#request("/api/cameras","POST",request);
    if(!object(value)||!string(value.camera_id)||!object(value.catalog_revision)||!string(value.catalog_revision.runtime_epoch)||!number(value.catalog_revision.generation))return invalid();
    return value as unknown as SaveCameraResponse;
  }
  /** People watching any of a camera's streams, or undefined when any count is unknown. */
  async viewers(cameraID:string) {
    const value=await this.#request(`/api/cameras/${encodeURIComponent(cameraID)}/status`);
    const counts=object(value)&&Array.isArray(value.streams)?value.streams.map(item=>object(item)?item.viewers:undefined):[];
    return counts.length&&counts.every(number)?(counts as number[]).reduce((sum,n)=>sum+n,0):undefined;
  }
  async reconnect(ref:StreamRef,request:Mutation) {
    const value=await this.#request(`/api/cameras/${encodeURIComponent(ref.camera_id)}/streams/${encodeURIComponent(ref.stream_id)}/recovery`,"POST",request);
    if(!object(value)||!Array.isArray(value.affected)||!Array.isArray(value.closed_sessions))return invalid();
    return value as unknown as ReconnectResponse;
  }
}
