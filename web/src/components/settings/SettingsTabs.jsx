import { Button } from '../ui/button.jsx';

export default function SettingsTabs({ activeSection }) {
  return (
    <div className="flex flex-wrap gap-2" data-testid="settings-tabs">
      <Button asChild variant={activeSection === 'account' ? 'secondary' : 'ghost'} size="sm">
        <a href="/settings">Account</a>
      </Button>
      <Button asChild variant={activeSection === 'ci' ? 'secondary' : 'ghost'} size="sm">
        <a href="/settings/ci">CI executors</a>
      </Button>
    </div>
  );
}
