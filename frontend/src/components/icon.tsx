import sprite from "./icons.svg?no-inline";
// Static geometry lives in one authenticated, hashed SVG asset. Stroke and fill
// inherit from each use, preserving the same currentColor icons in both themes.
const icon = (name:string, size=20) => () => <svg width={size} height={size} aria-hidden="true" focusable="false"><use href={`${sprite}#${name}`} /></svg>;
export const CameraIcon = icon("camera",24);
export const PlayIcon = icon("play");
export const PauseIcon = icon("pause");
export const StopIcon = icon("stop");
export const SidebarIcon = icon("sidebar");
export const KeyboardIcon = icon("keyboard");
export const CloseIcon = icon("close");
export const VolumeIcon = icon("volume");
export const MutedIcon = icon("muted");
export const FullscreenIcon = icon("fullscreen");
export const ExitFullscreenIcon = icon("exitfullscreen");
export const PipIcon = icon("pip");
export const PlusIcon = icon("plus");
export const RefreshIcon = icon("refresh");
