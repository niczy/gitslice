import AgentKeysCard from './AgentKeysCard.jsx';
import AuthContextCard from './AuthContextCard.jsx';
import AuthMethodsCard from './AuthMethodsCard.jsx';
import SessionsCard from './SessionsCard.jsx';

export default function AccountSettingsPanel(props) {
  return (
    <>
      <div className="grid gap-4 md:grid-cols-2">
        <AuthContextCard {...props} />
        <AuthMethodsCard {...props} />
        <SessionsCard {...props} />
      </div>

      <AgentKeysCard {...props} />
    </>
  );
}
