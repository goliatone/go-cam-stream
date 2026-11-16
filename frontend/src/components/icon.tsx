import sprite from "./icons.svg?no-inline";
// Static geometry lives in one authenticated, hashed SVG asset. Stroke and fill
// inherit from each use, preserving the same currentColor icons in both themes.
function Icon({name,size}:{name:string;size:number}) {
 return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><use href={`${sprite}#${name}`} /></svg>;
}
export const CameraIcon = () => <Icon name="camera" size={24} />;
export const PlayIcon = () => <Icon name="play" size={20} />;
export const PauseIcon = () => <Icon name="pause" size={20} />;
export const StopIcon = () => <Icon name="stop" size={20} />;
export const SidebarIcon = () => <Icon name="sidebar" size={20} />;
export const KeyboardIcon = () => <Icon name="keyboard" size={20} />;
export const CloseIcon = () => <Icon name="close" size={20} />;
export const VolumeIcon = () => <Icon name="volume" size={20} />;
export const MutedIcon = () => <Icon name="muted" size={20} />;
export const FullscreenIcon = () => <Icon name="fullscreen" size={20} />;
export const ExitFullscreenIcon = () => <Icon name="exitfullscreen" size={20} />;
export const PipIcon = () => <Icon name="pip" size={20} />;
