"""Exercise the actual AOS appear() fallback without importing ALAS/OCR libraries."""
import ast
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / 'rootfs/patches/module/base/base.py'
tree = ast.parse(SOURCE.read_text(encoding='utf-8'))
module_base = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == 'ModuleBase')
appear_node = next(node for node in module_base.body if isinstance(node, ast.FunctionDef) and node.name == 'appear')
template_node = next(node for node in module_base.body if isinstance(node, ast.FunctionDef) and node.name == 'match_template_color')
log_node = next(node for node in module_base.body if isinstance(node, ast.FunctionDef) and node.name == '_log_low_precision_match')


class Button:
    name = 'TEST_BUTTON'
    color = (255, 255, 255)

    def __init__(self, score, color_matches=True):
        self.score = score
        self.color_matches = color_matches
        self.similarities = []

    def match(self, image, offset, similarity):
        self.similarities.append(similarity)
        return self.score >= similarity

    def appear_on(self, image, threshold):
        return self.color_matches


class LowPrecisionRecognitionTests(unittest.TestCase):
    def setUp(self):
        self.enabled = False
        self.logger = Mock()
        def isfile(path):
            self.assertEqual(path, '/dev/shm/alasaos-low-precision')
            return self.enabled
        namespace = {'os': SimpleNamespace(path=SimpleNamespace(isfile=isfile)),
                     'logger': self.logger,
                     'HierarchyButton': type('HierarchyButton', (), {})}
        exec(compile(ast.Module(body=[appear_node, template_node, log_node], type_ignores=[]), str(SOURCE), 'exec'), namespace)
        self.appear = namespace['appear']
        self.match_template_color = namespace['match_template_color']
        self.log_match = namespace['_log_low_precision_match']
        self.hierarchy_button = namespace['HierarchyButton']
        self.host = SimpleNamespace(
            ensure_button=lambda button: button,
            device=SimpleNamespace(image=object(), stuck_record_add=lambda button: None),
            config=SimpleNamespace(BUTTON_OFFSET=(20, 20)),
        )
        self.host._log_low_precision_match = lambda button: self.log_match(self.host, button)

    def fail_twice_then_match(self, button):
        self.assertFalse(self.appear(self.host, button, offset=True))
        self.assertFalse(self.appear(self.host, button, offset=True))
        self.assertEqual(button.similarities, [0.85, 0.85])
        self.logger.info.assert_not_called()
        return self.appear(self.host, button, offset=True)

    def test_strict_mode_does_not_accept_a_low_score(self):
        button = Button(0.72)
        self.assertFalse(self.appear(self.host, button, offset=True))
        self.assertEqual(button.similarities, [0.85])

    def test_enabling_and_disabling_is_observed_by_the_same_running_task(self):
        button = Button(0.72)
        self.enabled = True
        self.assertTrue(self.fail_twice_then_match(button))
        self.assertEqual(button.similarities, [0.85, 0.85, 0.85, 0.70])
        self.enabled = False
        self.assertFalse(self.appear(self.host, button, offset=True))
        self.enabled = True
        self.assertFalse(self.appear(self.host, button, offset=True))
        self.assertFalse(self.appear(self.host, button, offset=True))
        self.assertTrue(self.appear(self.host, button, offset=True))

    def test_relaxed_mode_keeps_the_color_guard(self):
        self.enabled = True
        self.assertFalse(self.fail_twice_then_match(Button(0.72, color_matches=False)))
        self.logger.info.assert_not_called()

    def test_relaxed_mode_rejects_scores_below_the_existing_floor(self):
        self.enabled = True
        self.assertFalse(self.fail_twice_then_match(Button(0.69)))
        self.logger.info.assert_not_called()

    def test_explicit_similarity_and_color_only_detection_are_unchanged(self):
        self.enabled = True
        button = Button(0.88)
        self.assertFalse(self.appear(self.host, button, offset=True, similarity=0.9))
        self.assertEqual(button.similarities, [0.9])
        button = Button(0.1)
        self.assertTrue(self.appear(self.host, button, offset=False))
        self.assertEqual(button.similarities, [])
        self.logger.info.assert_not_called()

    def test_strict_success_does_not_trigger_the_fallback(self):
        self.enabled = True
        button = Button(0.99)
        self.assertTrue(self.appear(self.host, button, offset=True))
        self.assertEqual(button.similarities, [0.85])
        self.logger.info.assert_not_called()

    def test_repeated_matches_of_the_same_target_log_only_once(self):
        self.enabled = True
        for score in (0.99, 0.72, 0.69):
            self.appear(self.host, Button(score), offset=True)
        self.appear(self.host, Button(0.72), offset=True)
        self.appear(self.host, Button(0.99), offset=False)
        self.logger.info.assert_called_once_with('低精度识别：TEST_BUTTON')

    def test_each_distinct_target_logs_once_even_when_interleaved(self):
        self.enabled = True
        first = Button(0.72)
        second = Button(0.75)
        second.name = 'SECOND_BUTTON'
        for _ in range(100):
            self.appear(self.host, first, offset=True)
            self.appear(self.host, second, offset=True)
        self.assertEqual([call.args[0] for call in self.logger.info.call_args_list], [
            '低精度识别：TEST_BUTTON',
            '低精度识别：SECOND_BUTTON',
        ])

    def test_disabled_mode_does_not_log_image_matches(self):
        self.appear(self.host, Button(0.99), offset=True)
        self.appear(self.host, Button(0.69), offset=False)
        self.logger.info.assert_not_called()

    def test_interval_skips_and_hierarchy_queries_do_not_log_recognition(self):
        self.enabled = True
        button = Button(0.72)
        self.host.interval_timer = {button.name: SimpleNamespace(limit=1, reached=lambda: False)}
        self.assertFalse(self.appear(self.host, button, offset=True, interval=1))
        self.assertEqual(button.similarities, [])
        self.appear(self.host, self.hierarchy_button())
        self.logger.info.assert_not_called()
        self.assertTrue(self.fail_twice_then_match(button))
        self.logger.info.assert_called_once_with('低精度识别：TEST_BUTTON')

    def test_strict_color_template_matches_never_report_low_precision(self):
        button = Button(0.99)
        button.match_template_color = Mock(return_value=True)
        self.match_template_color(self.host, button)
        self.logger.info.assert_not_called()
        self.enabled = True
        for result in (True, False):
            button.match_template_color.return_value = result
            self.assertEqual(self.match_template_color(self.host, button), result)
        self.logger.info.assert_not_called()
        self.host.interval_timer = {button.name: SimpleNamespace(limit=1, reached=lambda: False)}
        self.assertFalse(self.match_template_color(self.host, button, interval=1))
        self.logger.info.assert_not_called()

    def test_new_running_instance_reports_the_target_again(self):
        self.enabled = True
        self.assertTrue(self.fail_twice_then_match(Button(0.72)))
        self.logger.reset_mock()
        self.host = SimpleNamespace(**{
            name: value for name, value in vars(self.host).items()
            if name not in ('_low_precision_logged_targets', '_low_precision_failures')
        })
        self.assertTrue(self.fail_twice_then_match(Button(0.72)))
        self.logger.info.assert_called_once_with('低精度识别：TEST_BUTTON')

    def test_normal_success_resets_consecutive_failures(self):
        self.enabled = True
        button = Button(0.72)
        for _ in range(2):
            self.assertFalse(self.appear(self.host, button, offset=True))
        button.score = 0.99
        self.assertTrue(self.appear(self.host, button, offset=True))
        button.score = 0.72
        button.similarities.clear()
        self.assertTrue(self.fail_twice_then_match(button))

    def test_failure_counts_are_per_target(self):
        self.enabled = True
        first, second = Button(0.72), Button(0.72)
        second.name = 'SECOND_BUTTON'
        self.assertFalse(self.appear(self.host, first, offset=True))
        self.assertFalse(self.appear(self.host, first, offset=True))
        self.assertFalse(self.appear(self.host, second, offset=True))
        self.assertTrue(self.appear(self.host, first, offset=True))
        self.assertEqual(second.similarities, [0.85])

    def test_strict_color_template_success_resets_consecutive_failures(self):
        self.enabled = True
        button = Button(0.72)
        for _ in range(2):
            self.assertFalse(self.appear(self.host, button, offset=True))
        button.match_template_color = Mock(return_value=True)
        self.assertTrue(self.match_template_color(self.host, button))
        button.similarities.clear()
        self.assertTrue(self.fail_twice_then_match(button))


if __name__ == '__main__':
    unittest.main()
