/**
 * Sample TypeScript script for keypooler.
 *
 * Usage by keypooler:
 *   bun trigger.ts --function=greet --input='{"name":"world"}'
 *
 * The API key is available via KEYPOOLER_API_KEY env var.
 * Output must be JSON to stdout: {"success": true, "data": {...}}
 */

interface Input {
  name?: string;
}

interface Result {
  success: boolean;
  data?: Record<string, unknown>;
  error?: string;
}

function greet(input: Input): Result {
  const name = input.name ?? "world";
  const apiKey = process.env.KEYPOOLER_API_KEY ?? "";
  const keyPreview = apiKey.length > 8 ? apiKey.slice(0, 8) + "..." : apiKey;

  return {
    success: true,
    data: {
      message: `Hello, ${name}!`,
      key_received: Boolean(apiKey),
      key_preview: keyPreview,
    },
  };
}

const FUNCTIONS: Record<string, (input: Input) => Result> = {
  greet,
};

function main() {
  const args = process.argv.slice(2);
  let functionName = "";
  let inputJSON = "{}";

  for (const arg of args) {
    if (arg.startsWith("--function=")) {
      functionName = arg.slice("--function=".length);
    } else if (arg.startsWith("--input=")) {
      inputJSON = arg.slice("--input=".length);
    }
  }

  if (!functionName) {
    console.log(JSON.stringify({ success: false, error: "missing --function argument" }));
    process.exit(1);
  }

  const fn = FUNCTIONS[functionName];
  if (!fn) {
    console.log(JSON.stringify({ success: false, error: `unknown function: ${functionName}` }));
    process.exit(1);
  }

  let input: Input;
  try {
    input = JSON.parse(inputJSON);
  } catch (e) {
    console.log(JSON.stringify({ success: false, error: `invalid input JSON: ${e}` }));
    process.exit(1);
  }

  const result = fn(input);
  console.log(JSON.stringify(result));
}

main();
