export const KEY = "fullsend_admin_org_list_refresh_after_install";

export function setPendingOrgListRefreshAfterInstall(): void {
  sessionStorage.setItem(KEY, "1");
}

/** Returns true the first time after install return; then clears the flag. */
export function consumePendingOrgListRefresh(): boolean {
  const v = sessionStorage.getItem(KEY);
  sessionStorage.removeItem(KEY);
  return v === "1";
}
