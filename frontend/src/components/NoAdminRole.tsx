import { Card, CardContent } from "@/components/ui/Card";
import { T } from "@/lib/ui/typography";

export const NO_ADMIN_ROLE_TITLE = "This account holds no admin role.";

/** What a principal without the admin audience sees instead of an admin view. */
export function NoAdminRole() {
  return (
    <Card>
      <CardContent className="space-y-1 px-5 py-4">
        <p className="text-sm font-medium">{NO_ADMIN_ROLE_TITLE}</p>
        <p className={T.hint}>
          The console&apos;s management views need an admin role. Objects are
          reached through the API and the SDKs.
        </p>
      </CardContent>
    </Card>
  );
}
