import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogTitle,
} from "./alert-dialog";

function open(action: React.ReactNode) {
  render(
    <AlertDialog open>
      <AlertDialogContent>
        <AlertDialogTitle>Delete?</AlertDialogTitle>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        {action}
      </AlertDialogContent>
    </AlertDialog>,
  );
}

describe("AlertDialogAction", () => {
  // Eleven delete confirmations override the background this way; with the
  // class only concatenated, the variant's bg-primary won and every one of
  // them rendered as an ordinary primary button.
  it("lets a className override the variant's background", () => {
    open(
      <AlertDialogAction className="bg-destructive">Delete</AlertDialogAction>,
    );
    const button = screen.getByRole("button", { name: "Delete" });
    expect(button.className).toContain("bg-destructive");
    expect(button.className).not.toMatch(/(^|\s)bg-primary(\s|$)/);
  });

  it("takes the destructive variant", () => {
    open(<AlertDialogAction variant="destructive">Delete</AlertDialogAction>);
    expect(
      screen.getByRole("button", { name: "Delete" }).className,
    ).not.toMatch(/(^|\s)bg-primary(\s|$)/);
  });
});
