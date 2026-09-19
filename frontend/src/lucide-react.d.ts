// lucide-react 1.x publishes declarations under `typings`, which TypeScript's
// bundler resolution does not use for this package. Keep the existing icon
// imports type-safe enough for the production bundle until the package fixes
// its export metadata.
declare module "lucide-react" {
  import type { ComponentType } from "react";
  export const Activity: ComponentType<any>;
  export const Bot: ComponentType<any>;
  export const Boxes: ComponentType<any>;
  export const BrainCircuit: ComponentType<any>;
  export const Check: ComponentType<any>;
  export const CheckIcon: ComponentType<any>;
  export const ChevronDown: ComponentType<any>;
  export const ChevronRightIcon: ComponentType<any>;
  export const CircleDot: ComponentType<any>;
  export const FolderGit2: ComponentType<any>;
  export const Gauge: ComponentType<any>;
  export const GitCompareArrows: ComponentType<any>;
  export const Key: ComponentType<any>;
  export const Download: ComponentType<any>;
  export const LayoutDashboard: ComponentType<any>;
  export const LoaderCircle: ComponentType<any>;
  export const Menu: ComponentType<any>;
  export const Moon: ComponentType<any>;
  export const PanelLeftIcon: ComponentType<any>;
  export const Play: ComponentType<any>;
  export const ShieldAlert: ComponentType<any>;
  export const ShieldCheck: ComponentType<any>;
  export const Search: ComponentType<any>;
  export const Sun: ComponentType<any>;
  export const Trash2: ComponentType<any>;
  export const User: ComponentType<any>;
  export const Wrench: ComponentType<any>;
  export const X: ComponentType<any>;
  export const XIcon: ComponentType<any>;
}
