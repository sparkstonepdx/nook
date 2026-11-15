import { PageHead } from "../components";
import { Backups as BackupPanel } from "../backups";

// Server-wide backups: setup, the last run, Back up now. Each app's switch and Restore stay in its Manage sheet.
export default function Backups() {
  return (
    <>
      <PageHead title="Backups" />
      <p class="lede">To leave an app out or restore it, open its Manage sheet.</p>
      <BackupPanel />
    </>
  );
}
