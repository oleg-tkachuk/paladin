import { SECTION } from "@/lib/navigation/sections";

/**
 * The signed-in user's own pages: their account, and what they let act as
 * them. Every one reads IAM alone, so it works without the admin plane; the
 * route guard, the sidebar's Account group, the user menu and the command
 * palette all take them from here, in this order. Paths and words only: the
 * route guard reads the paths, and the menus add their icons
 * (ACCOUNT_ICONS).
 */
export interface AccountPage {
  name: string;
  path: string;
  /** What the page answers, where a list of pages has room to say. */
  description: string;
}

export const ACCOUNT_PAGES = [
  {
    name: "Profile",
    path: SECTION.profile,
    description: "Your account, password and console preferences",
  },
  {
    name: "Connected applications",
    path: SECTION.applications,
    description: "Agents and tools you let act as you through OAuth",
  },
  {
    name: "My capabilities",
    path: SECTION.myCapabilities,
    description: "What those applications may do as you, and until when",
  },
] as const satisfies readonly AccountPage[];

/** An account page's path. */
export type AccountPath = (typeof ACCOUNT_PAGES)[number]["path"];
