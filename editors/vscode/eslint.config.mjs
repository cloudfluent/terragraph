import eslint from "@eslint/js";
import prettier from "eslint-config-prettier";
import { defineConfig } from "eslint/config";
import tseslint from "typescript-eslint";

export default defineConfig(
  {
    ignores: ["bin/**", "node_modules/**", "out/**", "*.vsix"],
  },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  prettier,
);
