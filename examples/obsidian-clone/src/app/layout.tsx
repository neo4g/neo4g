import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Obsidian Clone — Neo4g",
  description: "A graph-powered note-taking app built on Neo4g",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="bg-obsidian-bg text-obsidian-text antialiased">
        {children}
      </body>
    </html>
  );
}
