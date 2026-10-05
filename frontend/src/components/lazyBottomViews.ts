import { lazyView } from '../utils/lazyView';

// Bottom-dock and tab views, shared by BottomDock and ResourceListTabContent.
// The terminal carries xterm, the editor Monaco's wrapper and js-yaml: none of
// it belongs on the startup path.
export const PodLogs = lazyView(() => import('./PodLogs'));
export const DeploymentLogs = lazyView(() => import('./DeploymentLogs'));
export const PodShell = lazyView(() => import('./PodShell'));
export const NodeShell = lazyView(() => import('./NodeShell'));
export const TerminalContainer = lazyView(() => import('./TerminalContainer'));
export const YamlEditor = lazyView(() => import('./YamlEditor'));
export const CrossplaneTrace = lazyView(() => import('./CrossplaneTrace'));
