export default function AppFooter({ docsUrl, statusUrl, supportUrl, githubUrl }) {
  return (
    <>
      <footer className="footer bg-card/70" aria-label="Global footer">
        <p className="footer-copy">Git Slice • Slice smart. Ship faster.</p>
        <nav className="footer-links" aria-label="Self-service links">
          <a href={docsUrl} target="_blank" rel="noreferrer">Docs</a>
          <a href={statusUrl} target="_blank" rel="noreferrer">Status</a>
          <a href={supportUrl} target="_blank" rel="noreferrer">Support</a>
          <a href={githubUrl} target="_blank" rel="noreferrer">GitHub</a>
        </nav>
      </footer>
    </>
  );
}
