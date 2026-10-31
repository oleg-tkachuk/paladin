import type { ElementType } from "react";
import {
  IdentificationIcon,
  KeyIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import { SECTION } from "@/lib/navigation/sections";

/**
 * The signed-in user's own pages: their account, and what they let act as
 * them. Every one reads IAM alone, so it works without the admin plane; the
 * sidebar's Account group, the user menu and the command palette all list
 * these, in this order.
 */
export interface AccountPage {
  name: string;
  path: string;
  /** What the page answers, where a list of pages has room to say. */
  description: string;
  icon: ElementType;
}

export const ACCOUNT_PAGES: readonly AccountPage[] = [
  {
    name: "Profile",
    path: SECTION.profile,
    description: "Your account, password and console preferences",
    icon: UserCircleIcon,
  },
  {
    name: "Connected applications",
    path: SECTION.applications,
    description: "Agents and tools you let act as you through OAuth",
    icon: KeyIcon,
  },
  {
    name: "My capabilities",
    path: SECTION.myCapabilities,
    description: "What those applications may do as you, and until when",
    icon: IdentificationIcon,
  },
];
