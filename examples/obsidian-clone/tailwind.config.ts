import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        obsidian: {
          bg: "#1e1e1e",
          sidebar: "#252525",
          editor: "#2d2d2d",
          border: "#3d3d3d",
          text: "#dcddde",
          muted: "#888888",
          accent: "#7c6af7",
          "accent-hover": "#9580ff",
          input: "#1e1e1e",
        },
      },
    },
  },
  plugins: [],
};

export default config;
