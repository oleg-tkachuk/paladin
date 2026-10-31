import type { ElementType } from "react";
import {
  IdentificationIcon,
  KeyIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import type { AccountPath } from "@/lib/navigation/account";
import { SECTION } from "@/lib/navigation/sections";

/** Each account page's icon, wherever a list of them shows one. */
export const ACCOUNT_ICONS = {
  [SECTION.profile]: UserCircleIcon,
  [SECTION.applications]: KeyIcon,
  [SECTION.myCapabilities]: IdentificationIcon,
} as const satisfies Record<AccountPath, ElementType>;
