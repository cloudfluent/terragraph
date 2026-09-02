import eslint from "@eslint/js";
import prettier from "eslint-config-prettier";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: ["bin/**", "node_modules/**", "out/**", "*.vsix"],
  },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  prettier,
);
